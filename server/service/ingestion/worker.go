package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
	"google.golang.org/protobuf/proto"
	"log"
	"log/slog"
	"time"
)

// ingestionLog is the logger for resolution and plugin orchestration
// (category server/service/ingestion). Set by RunWorker; when nil,
// resolve.go falls back to slog.Default().
var ingestionLog *slog.Logger

// WorkerOptions configures RunWorker. All fields except DB and Queue are
// optional: a nil Counter, Logger, Trigger, or Registry is treated as "not
// set" and the worker degrades gracefully (no telemetry, default logger,
// no downstream nudge).
type WorkerOptions struct {
	// DB is the database abstraction the worker reads jobs from and writes
	// results to. Required.
	DB db.DB
	// Queue is the channel of job requests to process. The worker exits
	// when the channel is closed. Required.
	Queue <-chan *JobRequest
	// IdentifierRegistry is the identifier plugin registry used during
	// instrument resolution.
	IdentifierRegistry *identifier.Registry
	// DescriptionRegistry is the description plugin registry used to
	// extract identifier hints from broker descriptions.
	DescriptionRegistry *description.Registry
	// Counter is an optional metrics counter; nil disables telemetry.
	Counter telemetry.CounterIncrementer
	// Logger is the slog logger for ingestion-side logging; nil falls back
	// to slog.Default().
	Logger *slog.Logger
	// PriceTrigger is fired after a tx import that produced new state, and
	// after a successful price import; nil disables price-fetcher nudging.
	PriceTrigger chan<- struct{}
	// CorporateEventTrigger is fired after a successful corporate event
	// import; nil disables corporate-event-fetcher nudging.
	CorporateEventTrigger chan<- struct{}
	// TransferMatchTrigger is fired after a tx import that produced new state;
	// nil disables transfer-matcher nudging.
	TransferMatchTrigger chan<- struct{}
	// Workers is the per-process worker status registry shown in the admin
	// UI; nil disables status reporting.
	Workers *worker.Registry
}

// RunWorker processes job requests from opts.Queue until the channel is
// closed or ctx is cancelled. Resolution uses DB, then in-batch cache, then
// description plugins (extract hints) and identifier plugins (timeout from
// config, retry once with backoff).
func RunWorker(ctx context.Context, opts WorkerOptions) {
	ingestionLog = opts.Logger
	const name = "ingestion"
	if opts.Workers != nil {
		opts.Workers.SetIdle(name)
	}
	for {
		if opts.Workers != nil {
			opts.Workers.SetQueueDepth(name, len(opts.Queue))
		}
		select {
		case <-ctx.Done():
			return
		case j, ok := <-opts.Queue:
			if !ok {
				return
			}
			if opts.Workers != nil {
				opts.Workers.SetRunning(name, fmt.Sprintf("Processing job %s", j.JobID))
				opts.Workers.SetQueueDepth(name, len(opts.Queue))
			}
			processJob(ctx, opts, j)
			if opts.Workers != nil {
				opts.Workers.SetIdle(name)
			}
		}
	}
}

func processJob(ctx context.Context, opts WorkerOptions, j *JobRequest) {
	_ = opts.DB.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)

	switch j.JobType {
	case db.JobTypeTx:
		if ok, userID := processTx(ctx, opts.DB, opts.IdentifierRegistry, opts.DescriptionRegistry, opts.Counter, j); ok {
			if err := recalcAfterIngestion(ctx, opts.DB, userID); err != nil {
				log.Printf("ingestion job %s: recalc INITIALIZE txs: %v", j.JobID, err)
			}
			// The corporate event fetcher is not nudged because splits are
			// not time-critical -- the daily corporate event fetch cycle is
			// sufficient for any newly resolved instruments.
			pluginutil.Trigger(opts.PriceTrigger)
			// Fired after the store has committed, which is what the matcher
			// needs: it reads stored postings, not this job's payload. The
			// import may also have supplied the second side of a transfer
			// whose first side arrived months ago.
			pluginutil.Trigger(opts.TransferMatchTrigger)
		}
	case db.JobTypeSystemArchive:
		res := processSystemImport(ctx, opts.DB, opts.IdentifierRegistry, j)
		// Nudged once for the whole import rather than per part. Instruments
		// trigger nothing: an imported instrument has no holdings yet, so it
		// opens no price gap.
		if res.pricesPersisted {
			pluginutil.Trigger(opts.PriceTrigger)
		}
		if res.eventsPersisted {
			pluginutil.Trigger(opts.CorporateEventTrigger)
		}
	case db.JobTypeUserArchive:
		deps := ingestDeps{
			DB:           opts.DB,
			Registry:     opts.IdentifierRegistry,
			DescRegistry: opts.DescriptionRegistry,
			Counter:      opts.Counter,
		}
		res := processUserImport(ctx, deps, j)
		// Nudged once for the whole import rather than per part.
		//
		// Restored postings earn what a broker upload earns: the pads a
		// declaration implies are recomputed, the price fetcher is asked to fill
		// the gaps the new holdings opened, and the transfer matcher is given a
		// chance at a side whose counterpart was already stored. A restored
		// display currency opens FX gaps of its own, so either alone is reason
		// enough to nudge the price fetcher.
		if res.txsStored {
			if err := recalcAfterIngestion(ctx, opts.DB, res.userID); err != nil {
				log.Printf("user archive job %s: recalc INITIALIZE txs: %v", j.JobID, err)
			}
			pluginutil.Trigger(opts.TransferMatchTrigger)
		}
		if res.displayCurrencySet || res.txsStored {
			pluginutil.Trigger(opts.PriceTrigger)
		}
	default:
		log.Printf("ingestion job %s: unknown job type %q", j.JobID, j.JobType)
		_ = opts.DB.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
	}
}

// finishJob sets a job's terminal status and only then discards its payload.
//
// The order matters. The payload is what a job is; clearing it before the work
// is done means a service restarted mid-job re-enqueues a row whose payload is
// NULL, which unmarshals cleanly as an empty request, imports nothing and
// reports SUCCESS. Clearing it at the end instead makes the recovery path at
// startup able to actually redo the work, and the imports are built from
// idempotent upserts so redoing it is safe.
func finishJob(ctx context.Context, database db.DB, jobID string, status apiv1.JobStatus) {
	_ = database.SetJobStatus(ctx, jobID, status)
	_ = database.ClearJobPayload(ctx, jobID)
}

func processTx(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) (bool, string) {
	// Look up userID from the job row.
	var userID string
	if d, err := database.GetJob(ctx, j.JobID); err == nil {
		userID = d.UserID
	}

	payload, err := database.LoadJobPayload(ctx, j.JobID)
	if err != nil {
		log.Printf("ingestion job %s: load payload: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	var req ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("ingestion job %s: unmarshal payload: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}

	txs := req.GetTxs()
	if txs == nil {
		txs = []*apiv1.Tx{}
	}
	// A tx job has no part rows, so its reporter is the unscoped kind: it writes
	// progress and problems against the job itself.
	rep := archiveimport.NewPartReporter(database, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED)

	// The upload declares one basis for every row it carries, so the per-row
	// slice the store takes is that value repeated. A file that restates only
	// some of its rows is the archive's shape, not the CSV's.
	var basis []*time.Time
	if b := req.GetShareCountBasis(); b != "" {
		parsed, err := time.Parse("2006-01-02", b)
		if err != nil {
			rep.Errf(-1, "share_count_basis", fmt.Sprintf("invalid date %q: want YYYY-MM-DD", b))
			rep.Flush(ctx)
			finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
			return false, ""
		}
		basis = make([]*time.Time, len(txs))
		for i := range basis {
			basis[i] = &parsed
		}
	}

	res, err := ingestBatch(ctx, ingestDeps{
		DB:           database,
		Registry:     registry,
		DescRegistry: descRegistry,
		Counter:      counter,
	}, ingestParams{
		UserID:          userID,
		Broker:          db.BrokerToStr(req.Broker),
		Source:          req.GetSource(),
		JobID:           j.JobID,
		Txs:             txs,
		ShareCountBasis: basis,
		// Both bounds present makes this a replacement rather than an append.
		// CreateTx wraps its one tx in the same request with no period, which is
		// where the two paths diverge.
		PeriodFrom:   req.PeriodFrom,
		PeriodBefore: req.PeriodBefore,
	}, rep)
	// Flushed before the job is marked terminal on both paths: the problems
	// gathered before a failure are what explain it.
	rep.Flush(ctx)
	if err != nil {
		log.Printf("ingestion job %s: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}

	// Recompute split-adjusted values for instruments that have split rows.
	// The INSERT trigger on txs copies raw values; this pass corrects them.
	recomputeSplitAdjustedTxs(ctx, database, res.InstrumentIDs)

	finishJob(ctx, database, j.JobID, apiv1.JobStatus_SUCCESS)
	return true, userID
}

// recomputeSplitAdjustedTxs checks which of the given instrument IDs have
// stock_splits rows and recomputes their split_adjusted_* tx columns.
func recomputeSplitAdjustedTxs(ctx context.Context, database db.DB, instrumentIDs []string) {
	unique := make(map[string]bool, len(instrumentIDs))
	var deduped []string
	for _, id := range instrumentIDs {
		if id != "" && !unique[id] {
			unique[id] = true
			deduped = append(deduped, id)
		}
	}
	if len(deduped) == 0 {
		return
	}
	withSplits, err := database.InstrumentsWithSplits(ctx, deduped)
	if err != nil {
		log.Printf("recompute split-adjusted txs: %v", err)
		return
	}
	for _, id := range withSplits {
		if err := database.RecomputeSplitAdjustments(ctx, id); err != nil {
			log.Printf("recompute split-adjusted txs for %s: %v", id, err)
		}
	}
}

// filterStoredTxs returns only txs with stored types that are not ignored, along with their original indices.
func filterStoredTxs(txs []*apiv1.Tx, broker string, ignored []db.IgnoredAssetClass) ([]*apiv1.Tx, []int) {
	var filtered []*apiv1.Tx
	var indices []int
	for i, tx := range txs {
		if !TxTypeStored(tx.Type) {
			continue
		}
		if TxIgnored(tx, broker, ignored) {
			continue
		}
		filtered = append(filtered, tx)
		indices = append(indices, i)
	}
	return filtered, indices
}

// extractDescHints looks up each distinct (source, description) in DB and runs
// batch description extraction for misses. Returns a resolve cache pre-populated
// with DB hits and an extracted hints cache keyed by cacheKey(source, desc).
//
// A description every one of whose postings already names an identifier is not
// extracted. Extraction exists to find an identifier in a description, so it has
// nothing to add there, and it is a paid plugin call. It matters most to an
// archive import, whose every posting carries the identifier the export chose
// under a source no stored description was resolved against: without this the
// lookup would miss for the whole document and pay for extracting it. The test
// is over every posting sharing the description, so one posting arriving without
// a hint still gets the description extracted.
func extractDescHints(ctx context.Context, database db.DB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, source, broker string, txs []*apiv1.Tx) (map[string]resolveResult, map[string][]identifier.Identifier, error) {
	cache := make(map[string]resolveResult)
	var extractedHintsCache map[string][]identifier.Identifier
	needsExtraction := make(map[string]bool)
	for _, tx := range txs {
		if len(tx.GetIdentifierHints()) == 0 {
			needsExtraction[cacheKey(source, tx.GetInstrumentDescription())] = true
		}
	}
	seen := make(map[string]bool)
	var batchItems []description.BatchItem
	idByKey := make(map[string]string)
	for _, tx := range txs {
		desc := tx.GetInstrumentDescription()
		key := cacheKey(source, desc)
		if seen[key] {
			continue
		}
		seen[key] = true
		id, err := database.FindInstrumentBySourceDescription(ctx, source, desc)
		if err != nil {
			return nil, nil, err
		}
		if id != "" {
			cache[key] = resolveResult{InstrumentID: id}
		} else if needsExtraction[key] {
			batchID := shortHashForBatch(key)
			idByKey[key] = batchID
			batchItems = append(batchItems, description.BatchItem{
				ID:                    batchID,
				InstrumentDescription: desc,
				Hints:                 HintsFromTx(tx),
			})
		}
	}
	if len(batchItems) > 0 {
		hintsByID, err := runDescriptionPluginsBatch(ctx, database, descRegistry, counter, broker, source, batchItems)
		if err == nil && hintsByID != nil {
			extractedHintsCache = make(map[string][]identifier.Identifier)
			for key, id := range idByKey {
				extractedHintsCache[key] = hintsByID[id]
			}
		}
	}
	return cache, extractedHintsCache, nil
}

// resolveInstruments resolves each tx to an instrument ID using the pre-populated
// cache and extracted hints. Returns the instrument IDs (parallel to txs) and any
// identification errors collected from the cache.
func resolveInstruments(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source string, counter telemetry.CounterIncrementer, txs []*apiv1.Tx, originalIndices []int, cache map[string]resolveResult, extractedHintsCache map[string][]identifier.Identifier, rep *archiveimport.PartReporter) ([]string, []db.IdentificationError, error) {
	instrumentIDs := make([]string, len(txs))
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		rowIndex := int32(originalIndices[i])
		var hintsValidAt *time.Time
		if tx.GetTimestamp() != nil {
			t := tx.GetTimestamp().AsTime()
			hintsValidAt = &t
		}
		r, err := Resolve(ctx, database, registry, broker, source, desc, HintsFromTx(tx), identifierHintsFromTx(ctx, tx), cache, rowIndex, counter, extractedHintsCache, hintsValidAt)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", rowIndex, err)
		}
		instrumentIDs[i] = r.InstrumentID
		rep.Advance(ctx, 1)
	}
	var idErrs []db.IdentificationError
	for _, r := range cache {
		if r.IdErr != nil {
			idErrs = append(idErrs, *r.IdErr)
		}
	}
	return instrumentIDs, idErrs, nil
}
