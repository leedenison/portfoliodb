package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
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
	case db.JobTypePrice:
		if processPriceImport(ctx, opts.DB, opts.IdentifierRegistry, j) {
			pluginutil.Trigger(opts.PriceTrigger)
		}
	case db.JobTypeCorporateEvent:
		if processCorporateEventImport(ctx, opts.DB, opts.IdentifierRegistry, j) {
			// Only the corporate event fetcher is triggered here. The price
			// fetcher is not nudged because instruments resolved during
			// corporate event import cannot by definition create new holding
			// gaps that would require price data.
			pluginutil.Trigger(opts.CorporateEventTrigger)
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
	source := req.GetSource()
	broker, _ := brokerToStr(req.Broker)
	bulk := req.PeriodFrom != nil && req.PeriodBefore != nil

	// The denomination of the whole upload. Absent means as-traded: each row is
	// expressed in the share count current on its own transaction date.
	var shareCountBasis *time.Time
	if b := req.GetShareCountBasis(); b != "" {
		parsed, err := time.Parse("2006-01-02", b)
		if err != nil {
			_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{
				{RowIndex: -1, Field: "share_count_basis", Message: fmt.Sprintf("invalid date %q: want YYYY-MM-DD", b)},
			})
			finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
			return false, ""
		}
		shareCountBasis = &parsed
	}

	// Validate.
	errs := ValidateTxs(txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, errs)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Load ignored asset classes for this user.
	ignoredClasses, err := database.ListIgnoredAssetClasses(ctx, userID)
	if err != nil {
		log.Printf("ingestion job %s: load ignored asset classes: %v", j.JobID, err)
		ignoredClasses = nil // non-fatal: proceed without filtering
	}
	// Filter non-stored tx types (e.g. SPLIT) and ignored asset classes.
	txsToProcess, originalIndices := filterStoredTxs(txs, broker, ignoredClasses)
	if len(txsToProcess) == 0 {
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_SUCCESS)
		return true, userID
	}
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(txsToProcess)))
	// Extract description hints.
	cache, extractedHintsCache, err := extractDescHints(ctx, database, descRegistry, counter, source, broker, txsToProcess)
	if err != nil {
		log.Printf("ingestion job %s: extract description hints: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Resolve instruments.
	instrumentIDs, idErrs, err := resolveInstruments(ctx, database, registry, broker, source, j.JobID, counter, txsToProcess, originalIndices, cache, extractedHintsCache)
	if err != nil {
		log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "instrument_description", Message: err.Error()},
		})
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	if len(idErrs) > 0 {
		_ = database.AppendIdentificationErrors(ctx, j.JobID, idErrs)
	}
	// The resolved instruments feed both the asset-class check and balancing.
	instByID, err := instrumentsByID(ctx, database, instrumentIDs)
	if err != nil {
		log.Printf("ingestion job %s: load instruments: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Validate that each resolved instrument's asset class is compatible with
	// the asset class implied by the tx type. Catches contradictions that
	// arise when two txs share (source, description) but their tx types imply
	// different asset classes (e.g. BUYSTOCK + INCOME), as well as any other
	// path where resolution lands on an instrument of the wrong class.
	if classErrs := validateAssetClasses(txsToProcess, originalIndices, instrumentIDs, instByID); len(classErrs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, classErrs)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Balance every group by routing whatever its postings leave over to an
	// explicit counterparty. This runs after filtering, so a dropped SPLIT leg
	// cannot contribute a residual, and after resolution, because telling a
	// currency commodity from a security one is a property of the instrument.
	balanceInsts := balanceInstruments(instByID)
	routed := routeResiduals(txsToProcess, instrumentIDs, balanceInsts)
	routedTxs, routedIDs, routedWeights, unresolved := resolveRouted(ctx, database, routed)
	// A residual with no instrument to post it against used to be dropped with a log
	// line, leaving its group unbalanced. The balance constraint now rejects that
	// group at COMMIT, taking the whole upload with it, so failing the job here names
	// the commodity instead of surfacing a constraint violation that does not.
	// Currencies are seeded, so this is a safety net rather than a live path.
	if len(unresolved) > 0 {
		errs := make([]*apiv1.ValidationError, len(unresolved))
		for i, cur := range unresolved {
			log.Printf("ingestion job %s: no instrument for residual commodity %q", j.JobID, cur)
			errs[i] = &apiv1.ValidationError{
				RowIndex: -1,
				Field:    "instrument_description",
				Message:  fmt.Sprintf("no instrument for residual commodity %q; the group it balances cannot be stored", cur),
			}
		}
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, errs)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}
	// Weighed after routing, which is what assigns a synthetic group_ref to a posting
	// that had none, and before the routed postings are appended, since those carry
	// the weight of the residual they negate rather than one derived from the tx.
	txWeights := weights(txsToProcess, instrumentIDs, balanceInsts)
	txsToProcess = append(txsToProcess, routedTxs...)
	instrumentIDs = append(instrumentIDs, routedIDs...)
	txWeights = append(txWeights, routedWeights...)

	// Store transactions.
	var storeErr error
	if bulk {
		storeErr = database.ReplaceTxsInPeriod(ctx, userID, broker, j.JobID, req.PeriodFrom, req.PeriodBefore, txsToProcess, instrumentIDs, txWeights, shareCountBasis)
	} else {
		storeErr = database.CreateTxGroup(ctx, userID, broker, txsToProcess[0].GetAccount(), j.JobID, txsToProcess, instrumentIDs, txWeights, shareCountBasis)
	}
	if storeErr != nil {
		log.Printf("ingestion job %s: %v", j.JobID, storeErr)
		_ = database.AppendValidationErrors(ctx, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: storeErr.Error()},
		})
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false, ""
	}

	// Recompute split-adjusted values for instruments that have split rows.
	// The INSERT trigger on txs copies raw values; this pass corrects them.
	recomputeSplitAdjustedTxs(ctx, database, instrumentIDs)

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
func extractDescHints(ctx context.Context, database db.DB, descRegistry *description.Registry, counter telemetry.CounterIncrementer, source, broker string, txs []*apiv1.Tx) (map[string]resolveResult, map[string][]identifier.Identifier, error) {
	cache := make(map[string]resolveResult)
	var extractedHintsCache map[string][]identifier.Identifier
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
		} else {
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
func resolveInstruments(ctx context.Context, database db.DB, registry *identifier.Registry, broker, source, jobID string, counter telemetry.CounterIncrementer, txs []*apiv1.Tx, originalIndices []int, cache map[string]resolveResult, extractedHintsCache map[string][]identifier.Identifier) ([]string, []db.IdentificationError, error) {
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
		_ = database.IncrJobProcessedCount(ctx, jobID)
	}
	var idErrs []db.IdentificationError
	for _, r := range cache {
		if r.IdErr != nil {
			idErrs = append(idErrs, *r.IdErr)
		}
	}
	return instrumentIDs, idErrs, nil
}

// brokerToStr converts a proto Broker enum to its string representation.
// brokerToStr returns the stored form of a broker: its enum name.
func brokerToStr(b typev1.Broker) (string, error) {
	if b == typev1.Broker_BROKER_UNSPECIFIED {
		return "", fmt.Errorf("broker unspecified")
	}
	s, ok := typev1.Broker_name[int32(b)]
	if !ok {
		return "", fmt.Errorf("unknown broker")
	}
	return s, nil
}
