package ingestion

import (
	"context"
	"errors"
	"log"
	"time"

	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// errUnknownPart is a part row naming something this build cannot apply, which
// means a job written by a later build. It fails that part and leaves the rest
// alone rather than failing the whole import.
var errUnknownPart = errors.New("this build cannot apply that archive part")

// systemImportResult says which fetchers the import earned a nudge for. An
// instrument part triggers nothing: imported instruments have no holdings yet,
// so there are no price gaps to fill.
type systemImportResult struct {
	pricesPersisted bool
	eventsPersisted bool
	// failed says the job ended FAILED, which is what the run records. It is the
	// import's own verdict rather than a re-read of the job row, so the two
	// cannot drift.
	failed bool
}

// processSystemImport applies the parts of a system archive in restore order,
// recording a result per part.
//
// A failed part does not stop the sequence. The parts are not hard-dependent:
// the price and corporate event parts resolve any instrument they cannot find,
// so they work against an instance whose instrument part failed. Running
// instruments first is what stops them paying a plugin for instruments the file
// already describes -- an optimisation, not a prerequisite -- and abandoning
// the rest would throw away work that would otherwise land. Which parts did not
// apply is what the per-part results are for.
func processSystemImport(ctx context.Context, database db.DB, registry *identifier.Registry, j *JobRequest) systemImportResult {
	var out systemImportResult

	payload, err := database.LoadJobPayload(ctx, j.JobID)
	if err != nil {
		log.Printf("system archive job %s: load payload: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "payload", err.Error())
		out.failed = true
		return out
	}
	var a archivev1.SystemArchive
	if err := proto.Unmarshal(payload, &a); err != nil {
		log.Printf("system archive job %s: unmarshal payload: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "payload", err.Error())
		out.failed = true
		return out
	}

	// Knowledge time declared once for the whole document: every part reads it
	// the same way, which is the point of the envelope carrying it.
	var asOf *time.Time
	if ts := a.GetEnvelope().GetExportedAt(); ts != nil {
		t := ts.AsTime()
		asOf = &t
	}

	// One cache for the whole archive. An instrument named in both the price and
	// the corporate event part is the ordinary case, and resolving it twice means
	// paying a plugin twice.
	resolveCache := newResolveCache()

	// Which parts to run comes from the job's own rows rather than from the
	// document, so a job resumed after a restart skips what already finished.
	detail, err := database.GetJob(ctx, j.JobID)
	if err != nil {
		log.Printf("system archive job %s: read parts: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "job", err.Error())
		out.failed = true
		return out
	}

	anyFailed := false
	for _, pr := range detail.Parts {
		if pr.Status == apiv1.JobStatus_SUCCESS {
			// Already applied by a run this one is resuming.
			continue
		}
		// A part being re-run starts its progress over rather than counting on
		// from where the interrupted run left off.
		if pr.ProcessedCount > 0 || pr.Message != "" {
			_ = database.ResetJobPartProgress(ctx, j.JobID, pr.Part)
		}
		_ = database.SetJobPartStatus(ctx, j.JobID, pr.Part, apiv1.JobStatus_RUNNING)

		rep := archiveimport.NewPartReporter(database, j.JobID, pr.Part)
		var partErr error
		switch pr.Part {
		case archivev1.ArchivePart_INSTRUMENTS:
			_, partErr = archiveimport.InstrumentPart(ctx, database, a.GetInstruments(), rep)
		case archivev1.ArchivePart_PRICES:
			out.pricesPersisted, partErr = importPricePart(ctx, database, registry, a.GetPrices(), asOf, resolveCache, rep)
		case archivev1.ArchivePart_CORPORATE_EVENTS:
			out.eventsPersisted, partErr = importCorporateEventPart(ctx, database, registry, a.GetCorporateEvents(), asOf, resolveCache, rep)
		case archivev1.ArchivePart_INFLATION_INDICES:
			_, partErr = archiveimport.InflationPart(ctx, database, a.GetInflationIndices(), asOf, rep)
		case archivev1.ArchivePart_FETCH_BLOCKS:
			_, partErr = archiveimport.FetchBlockPart(ctx, database, a.GetFetchBlocks(), asOf, rep)
		case archivev1.ArchivePart_UNHANDLED_EVENTS:
			_, partErr = archiveimport.UnhandledEventPart(ctx, database, a.GetUnhandledEvents(), asOf, rep)
		case archivev1.ArchivePart_PLUGIN_CONFIG:
			_, partErr = archiveimport.PluginConfigPart(ctx, database, a.GetPluginConfig(), rep)
		default:
			partErr = errUnknownPart
		}
		// Flushed before the part is marked terminal on both paths: the errors
		// gathered before a hard failure are what explain it.
		rep.Flush(ctx)

		if partErr != nil {
			log.Printf("system archive job %s: part %s: %v", j.JobID, pr.Part, partErr)
			_ = database.SetJobPartFailed(ctx, j.JobID, pr.Part, partErr.Error())
			anyFailed = true
			continue
		}
		_ = database.SetJobPartStatus(ctx, j.JobID, pr.Part, apiv1.JobStatus_SUCCESS)
	}

	// The job's own counters are the sum over its parts, so a caller that only
	// knows about jobs still sees sensible progress.
	if final, err := database.GetJob(ctx, j.JobID); err == nil {
		var total, processed int32
		for _, pr := range final.Parts {
			total += pr.TotalCount
			processed += pr.ProcessedCount
		}
		_ = database.SetJobTotalCount(ctx, j.JobID, total)
		_ = database.SetJobProcessedCount(ctx, j.JobID, processed)
	}

	status := apiv1.JobStatus_SUCCESS
	if anyFailed {
		status = apiv1.JobStatus_FAILED
	}
	out.failed = anyFailed
	finishJob(ctx, database, j.JobID, status)
	return out
}

// failWholeJob records a failure that happened before any part could be reached,
// which belongs to the job rather than to a part.
func failWholeJob(ctx context.Context, database db.DB, jobID, field, message string) {
	_ = database.AppendValidationErrors(ctx, jobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED,
		[]*apiv1.ValidationError{{RowIndex: -1, Field: field, Message: message}})
	finishJob(ctx, database, jobID, apiv1.JobStatus_FAILED)
}
