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
	"github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/service/identification"
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
// optional: a nil TelemetryDB, Logger, Trigger, or Registry is treated as "not
// set" and the worker degrades gracefully (no telemetry, default logger, no
// downstream nudge).
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
	// CandidateRegistry is the candidate plugin registry used to
	// extract identifier hints from broker descriptions.
	CandidateRegistry *candidate.Registry
	// TelemetryDB records a run per job and the event rows beneath it; nil
	// disables recording. It is separate from DB because it holds its own pool
	// and must never join the work's transaction.
	TelemetryDB db.TelemetryDB
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
	// GroupingTrigger is fired alongside it, for the same reason: an import can
	// have supplied a leg that belongs with one stored months ago. nil disables
	// grouping nudging.
	GroupingTrigger chan<- struct{}
	// Workers is the per-process worker status registry shown in the admin
	// UI; nil disables status reporting.
	Workers *worker.Registry
}

// RunWorker processes job requests from opts.Queue until the channel is
// closed or ctx is cancelled. Resolution uses DB, then in-batch cache, then
// candidate plugins (extract hints) and identifier plugins (timeout from
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

// telemetryRunKind maps a job type onto the run kind that describes it. An empty
// return is a job type this build does not know, which opens no run: the
// vocabulary is closed, so there is no kind to file it under.
func telemetryRunKind(jobType string) string {
	switch jobType {
	case db.JobTypeTx:
		return db.TelemetryRunTxImport
	case db.JobTypeSystemArchive:
		return db.TelemetryRunSystemArchiveImport
	case db.JobTypeUserArchive:
		return db.TelemetryRunUserArchiveImport
	}
	return ""
}

func processJob(ctx context.Context, opts WorkerOptions, j *JobRequest) {
	_ = opts.DB.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)

	tel := opts.TelemetryDB
	if tel == nil {
		tel = db.NopTelemetry{}
	}
	// The job row, not the payload, is what the run's scope comes from. A job
	// whose payload will not load or unmarshal still leaves a run recording whose
	// upload it was and that it failed, which is the case most worth seeing.
	var detail db.JobDetail
	if d, err := opts.DB.GetJob(ctx, j.JobID); err == nil && d != nil {
		detail = *d
	}
	var runID string
	kind := telemetryRunKind(j.JobType)
	if kind != "" {
		runID = tel.StartRun(ctx, db.TelemetryRun{
			Kind:   kind,
			JobID:  j.JobID,
			UserID: detail.UserID,
			Broker: detail.Broker,
			Source: detail.Source,
		})
	}
	// Failed unless something below says otherwise, so a path that returns without
	// deciding is recorded as the failure it is rather than as a success.
	outcome := db.TelemetryOutcomeFailed
	defer func() { tel.EndRun(ctx, runID, outcome) }()

	switch j.JobType {
	case db.JobTypeTx:
		if ok := processTx(ctx, ingestDeps{
			DB:                opts.DB,
			Registry:          opts.IdentifierRegistry,
			CandidateRegistry: opts.CandidateRegistry,
			Telemetry:         tel,
			RunID:             runID,
			RunKind:           kind,
		}, j, detail.UserID); ok {
			outcome = db.TelemetryOutcomeSuccess
			if err := recalcAfterIngestion(ctx, opts.DB, detail.UserID); err != nil {
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
			pluginutil.Trigger(opts.GroupingTrigger)
		}
	case db.JobTypeSystemArchive:
		res := processSystemImport(ctx, ingestDeps{
			DB:        opts.DB,
			Registry:  opts.IdentifierRegistry,
			Telemetry: tel,
			RunID:     runID,
			RunKind:   kind,
		}, j)
		if !res.failed {
			outcome = db.TelemetryOutcomeSuccess
		}
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
			DB:                opts.DB,
			Registry:          opts.IdentifierRegistry,
			CandidateRegistry: opts.CandidateRegistry,
			Telemetry:         tel,
			RunID:             runID,
			RunKind:           kind,
		}
		res := processUserImport(ctx, deps, j)
		if !res.failed {
			outcome = db.TelemetryOutcomeSuccess
		}
		// Nudged once for the whole import rather than per part.
		//
		// Restored postings earn what a broker upload earns: the pads a
		// declaration implies are recomputed, the price fetcher is asked to fill
		// the gaps the new holdings opened, and the transfer matcher is given a
		// chance at a side whose counterpart was already stored. A restored
		// display currency opens FX gaps of its own, so either alone is reason
		// enough to nudge the price fetcher.
		//
		// Restored declarations earn the recalc for their own sake rather than
		// the postings': a declaration carries no pad, and the pad is what makes
		// the declared quantity true. They earn the price fetch too, because the
		// holding a pad opens may have no postings of its own and therefore no
		// prices ever fetched for it.
		if res.txsStored {
			pluginutil.Trigger(opts.TransferMatchTrigger)
			pluginutil.Trigger(opts.GroupingTrigger)
		}
		if res.txsStored || res.declarationsStored {
			if err := recalcAfterIngestion(ctx, opts.DB, res.userID); err != nil {
				log.Printf("user archive job %s: recalc INITIALIZE txs: %v", j.JobID, err)
			}
		}
		if res.displayCurrencySet || res.txsStored || res.declarationsStored {
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

// processTx applies a broker upload. userID comes from the job row, which the
// caller has already read to scope the run, so it is passed in rather than read
// a second time.
func processTx(ctx context.Context, deps ingestDeps, j *JobRequest, userID string) bool {
	database := deps.DB
	payload, err := database.LoadJobPayload(ctx, j.JobID)
	if err != nil {
		log.Printf("ingestion job %s: load payload: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	var req ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		log.Printf("ingestion job %s: unmarshal payload: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}

	// A tx job has no part rows, so its reporter is the unscoped kind: it writes
	// progress and problems against the job itself.
	rep := archiveimport.NewPartReporter(database, j.JobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED)

	// The payload is an archive window, so a broker upload and an archive
	// document are read by the same code above the ingestion pipeline.
	w := req.GetWindow()
	txs, basis, rowIdx, err := windowTxs(w, 0, rep)
	if err != nil {
		rep.Flush(ctx)
		log.Printf("ingestion job %s: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}

	res, err := ingestBatch(ctx, deps, ingestParams{
		UserID:          userID,
		Broker:          db.BrokerToStr(w.GetBroker()),
		Source:          w.GetSource(),
		JobID:           j.JobID,
		Txs:             txs,
		ShareCountBasis: basis,
		RowIndices:      rowIdx,
		// The upload's own statement of when its identifiers were current. The
		// handler stamps its clock when the uploader said nothing, so this is
		// only nil for a payload written before the field existed.
		ExportedAt: vintage(req.GetExportedAt()),
		// Both bounds present makes this a replacement rather than an append.
		// CreateTx wraps its one posting in a window with no period, which is
		// where the two paths diverge.
		PeriodFrom:   w.GetPeriodFrom(),
		PeriodBefore: w.GetPeriodBefore(),
	}, rep)
	// Flushed before the job is marked terminal on both paths: the problems
	// gathered before a failure are what explain it.
	rep.Flush(ctx)
	if err != nil {
		log.Printf("ingestion job %s: %v", j.JobID, err)
		finishJob(ctx, database, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}

	// Recompute split-adjusted values for instruments that have split rows.
	// The INSERT trigger on txs copies raw values; this pass corrects them.
	recomputeSplitAdjustedTxs(ctx, database, res.InstrumentIDs)

	finishJob(ctx, database, j.JobID, apiv1.JobStatus_SUCCESS)
	return true
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

// proposeCandidates answers, for every distinct key in the batch, what the
// database already knows -- and asks the candidate plugins only about the rest.
//
// The grain is the resolution key: (source, description, stated identifiers).
// The pre-pass used to dedupe on (source, description) alone while the key
// ledger deduped with the hints, so the two agreed only when no posting carried
// a hint. Working at one grain is what lets a hinted key be answered here at
// all, and it is what the gate change needs: whether to pay a plugin for a key
// cannot be decided without knowing whether that key is already resolved.
//
// Both lookups happen here and nowhere else. A key with no stated identifiers
// is looked up by its description; a key with them is looked up by them. Resolve
// asks the database nothing further: a single match is in the cache, more than
// one is in conflicts, and neither is queried twice.
//
// What reaches the plugins is the key whose identity is incomplete rather than
// the key that stated nothing. A source that named a venue has said the last
// thing that changes which listing resolution lands on, and is left alone; a
// source that stated an ISIN and no venue has left the choice among that
// security's listings open, and is exactly what the stage exists to close. See
// identityComplete for where the line falls, and completesPartialIdentity for
// why only a broker upload is offered it.
//
// The returned outcome map is at key grain, and is stage one of each key's
// resolution record.
// prePass is what the pre-pass worked out about a batch, keyed throughout by
// cacheKeyWithHints. The four maps travel together because they are answers to
// one question asked once, and because separating them is how a caller comes to
// hold the resolutions without the conflicts -- reading a nil map yields false,
// so a conflicting key would resolve through the plugins instead of raising, and
// nothing would say so.
type prePass struct {
	// resolved is the keys the database already answered.
	resolved map[string]resolveResult
	// conflicts is the keys whose stated identifiers name more than one
	// instrument. Recorded rather than raised so one bad key does not stop the
	// lookups for the rest; Resolve raises it at the row that carries it.
	conflicts map[string]bool
	// proposed is what the candidate plugins offered, already filtered, with the
	// call each came from so a field can name both its parents.
	proposed map[string]keyProposals
	// outcome is what became of each key here, stage one of its resolution record.
	outcome map[string]string
}

func proposeCandidates(ctx context.Context, deps ingestDeps, source, broker string, txs []*apiv1.Tx, txHints [][]identifier.Identifier) (prePass, error) {
	database := deps.DB
	cache := make(map[string]resolveResult)
	conflicts := make(map[string]bool)
	var proposedHintsCache map[string]keyProposals
	outcome := make(map[string]string)
	fail := func(err error) (prePass, error) { return prePass{}, err }

	seen := make(map[string]bool)
	var batchItems []candidate.BatchItem
	idByKey := make(map[string]string)
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		key := cacheKeyWithHints(source, desc, txHints[i])
		if seen[key] {
			continue
		}
		seen[key] = true

		if len(txHints[i]) > 0 {
			ids, err := identification.ResolveIDsByHintsDBOnly(ctx, database, txHints[i])
			if err != nil {
				return fail(err)
			}
			switch {
			case len(ids) > 1:
				// Recorded rather than raised: one bad key must not stop the
				// lookups for the rest, and Resolve raises it at the row that
				// carries it, where the row index is known.
				conflicts[key] = true
				// A key that names two instruments is not a gap to be filled. It
				// is about to fail at the row that carries it, and a proposal
				// would be paid for and thrown away.
				outcome[key] = db.TelemetryCandidateNotAttemptedConflictingHints
				continue
			case len(ids) == 1:
				cache[key] = resolveResult{InstrumentID: ids[0], DBHitOutcome: db.TelemetryResolutionDBIdentifierHints}
				outcome[key] = db.TelemetryCandidateNotAttemptedDBHit
				continue
			}
			// The database did not recognise what the source stated, so what the
			// source stated is all there is -- and a plugin is asked to fill it
			// out only where it is genuinely partial.
			//
			// The two refusals are recorded apart. One says the gate did its job
			// and the source had already named a listing; the other says this kind
			// of run is never offered completion at all. They move for different
			// reasons and a rate that blends them says nothing.
			if !deps.completesPartialIdentity() {
				outcome[key] = db.TelemetryCandidateNotAttemptedRunKind
				continue
			}
			if identityComplete(txHints[i]) {
				outcome[key] = db.TelemetryCandidateNotAttemptedIdentityComplete
				continue
			}
		} else {
			id, err := database.FindInstrumentBySourceDescription(ctx, source, desc)
			if err != nil {
				return fail(err)
			}
			if id != "" {
				cache[key] = resolveResult{InstrumentID: id, DBHitOutcome: db.TelemetryResolutionDBSourceDescription}
				outcome[key] = db.TelemetryCandidateNotAttemptedDBHit
				continue
			}
		}
		batchID := shortHashForBatch(key)
		idByKey[key] = batchID
		batchItems = append(batchItems, candidate.BatchItem{
			ID:                    batchID,
			InstrumentDescription: desc,
			Stated:                txHints[i],
			Hints:                 HintsFromTx(tx),
		})
	}

	if len(batchItems) > 0 {
		proposedByID, itemOutcomes, err := runCandidatePluginsBatch(ctx, deps, broker, source, batchItems)
		if err == nil && proposedByID != nil {
			proposedHintsCache = make(map[string]keyProposals)
			for key, id := range idByKey {
				if kp, ok := proposedByID[id]; ok {
					proposedHintsCache[key] = kp
				}
			}
		}
		for key, id := range idByKey {
			outcome[key] = itemOutcomes[id]
		}
	}
	return prePass{resolved: cache, conflicts: conflicts, proposed: proposedHintsCache, outcome: outcome}, nil
}

// resolveInstruments resolves each tx to an instrument ID using the pre-populated
// cache and extracted hints. Returns the instrument IDs (parallel to txs) and any
// identification errors collected from the cache.
//
// exportedAt is the vintage every posting's identifier hints are stated as of,
// and it is one value for the batch rather than one per row: a file names an
// option under the symbol current when the file was written, not under the one
// the contract wore on each trade date. See docs/adr/0055-identifier-validity-is-an-interval.md
// for the convention and docs/spec/bitemporality.md for where it is recorded.
func resolveInstruments(ctx context.Context, deps ingestDeps, broker, source string, txs []*apiv1.Tx, originalIndices []int, txHints [][]identifier.Identifier, pre prePass, exportedAt *time.Time, rep *archiveimport.PartReporter) ([]string, []db.IdentificationError, error) {
	keys := newResolutionKeys(ctx, deps.Telemetry, deps.RunID, source, txs, txHints, pre)

	instrumentIDs := make([]string, len(txs))
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		rowIndex := int32(originalIndices[i])
		r, err := Resolve(ctx, deps.DB, deps.Registry, broker, source, desc, HintsFromTx(tx), txHints[i], pre, rowIndex, exportedAt, keys)
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", rowIndex, err)
		}
		instrumentIDs[i] = r.InstrumentID
		rep.Advance(ctx, 1)
	}
	var idErrs []db.IdentificationError
	for _, r := range pre.resolved {
		if r.IdErr != nil {
			idErrs = append(idErrs, *r.IdErr)
		}
	}
	return instrumentIDs, idErrs, nil
}
