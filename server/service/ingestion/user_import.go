package ingestion

import (
	"context"
	"log"

	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
)

// userImportResult says which fetchers the import earned a nudge for. A display
// currency that landed opens FX gaps the price fetcher has not been asked to
// fill, exactly as setting one through the API does, and stored postings earn
// whatever a broker upload earns.
type userImportResult struct {
	displayCurrencySet bool
	txsStored          bool
	// declarationsStored says whether any declaration landed. A restored
	// declaration is only half of what it means until its holding's pad is
	// settled from it, and which declaration pads a holding depends on the
	// others, so the recalc runs once for the whole import rather than per row.
	declarationsStored bool
	// userID is whose archive this was, for the recalc a stored posting earns.
	userID string
	// failed says the job ended FAILED, which is what the run records. It is the
	// import's own verdict rather than a re-read of the job row, so the two
	// cannot drift.
	failed bool
}

// processUserImport applies the parts of a user archive in restore order,
// recording a result per part. It applies to the job's own user: a user archive
// does not name one, so the account it restores into is whoever asked.
//
// A failed part does not stop the ones after it, for the same reason it does not
// in a system import: the parts are not hard-dependent, and abandoning the rest
// would throw away work that would otherwise land.
func processUserImport(ctx context.Context, deps ingestDeps, j *JobRequest) userImportResult {
	database := deps.DB
	var out userImportResult

	payload, err := database.LoadJobPayload(ctx, j.JobID)
	if err != nil {
		log.Printf("user archive job %s: load payload: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "payload", err.Error())
		out.failed = true
		return out
	}
	var a archivev1.UserArchive
	if err := proto.Unmarshal(payload, &a); err != nil {
		log.Printf("user archive job %s: unmarshal payload: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "payload", err.Error())
		out.failed = true
		return out
	}

	// Which parts to run comes from the job's own rows rather than from the
	// document, so a job resumed after a restart skips what already finished.
	detail, err := database.GetJob(ctx, j.JobID)
	if err != nil {
		log.Printf("user archive job %s: read parts: %v", j.JobID, err)
		failWholeJob(ctx, database, j.JobID, "job", err.Error())
		out.failed = true
		return out
	}

	out.userID = detail.UserID
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
		case archivev1.ArchivePart_PREFERENCES:
			var res archiveimport.PreferenceResult
			res, partErr = archiveimport.PreferencePart(ctx, database, detail.UserID, a.GetPreferences(), rep)
			out.displayCurrencySet = out.displayCurrencySet || res.DisplayCurrency
		case archivev1.ArchivePart_TXS:
			var stored bool
			stored, partErr = importTxPart(ctx, deps, detail.UserID, j.JobID, a.GetTxs(), rep)
			out.txsStored = out.txsStored || stored
		case archivev1.ArchivePart_DECLARATIONS:
			var written int32
			written, partErr = archiveimport.DeclarationPart(ctx, database, detail.UserID, a.GetDeclarations(), rep)
			out.declarationsStored = out.declarationsStored || written > 0
		default:
			partErr = errUnknownPart
		}
		// Flushed before the part is marked terminal on both paths: the errors
		// gathered before a hard failure are what explain it.
		rep.Flush(ctx)

		if partErr != nil {
			log.Printf("user archive job %s: part %s: %v", j.JobID, pr.Part, partErr)
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
