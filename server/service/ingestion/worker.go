package ingestion

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/worker"
)

// ingestionLog is the logger for resolution and plugin orchestration (category server/service/ingestion).
// Set by RunWorker; when nil, resolve.go falls back to slog.Default().
var ingestionLog *slog.Logger

// RunWorker processes job requests from the channel until it is closed.
// Resolution uses DB, then in-batch cache, then description plugins (extract hints) and identifier plugins (timeout from config, retry once with backoff).
// ingestionLogger is the logger for ingestion/resolution (typically with category server/service/ingestion); may be nil.
// priceTrigger is optional; when non-nil, a non-blocking signal is sent after each successful job to trigger price fetching.
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, ingestionLogger *slog.Logger, priceTrigger chan<- struct{}, workers *worker.Registry) {
	ingestionLog = ingestionLogger
	const name = "ingestion"
	if workers != nil {
		workers.SetIdle(name)
	}
	for {
		if workers != nil {
			workers.SetQueueDepth(name, len(queue))
		}
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			if workers != nil {
				workers.SetRunning(name, fmt.Sprintf("Processing job %s", j.JobID))
				workers.SetQueueDepth(name, len(queue))
			}
			processJob(ctx, database, registry, descRegistry, counter, j, priceTrigger)
			if workers != nil {
				workers.SetIdle(name)
			}
		}
	}
}

func processJob(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest, priceTrigger chan<- struct{}) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)
	if process(ctx, database, registry, descRegistry, counter, j) {
		if err := recalcAfterIngestion(ctx, database, j.UserID); err != nil {
			log.Printf("ingestion job %s: recalc INITIALIZE txs: %v", j.JobID, err)
		}
		pricefetcher.Trigger(priceTrigger)
	}
}

func process(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) bool {
	// Validate.
	errs := ValidateTxs(j.Txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	// Filter non-stored tx types (e.g. SPLIT).
	txsToProcess, originalIndices := filterStoredTxs(j.Txs)
	if len(txsToProcess) == 0 {
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
		return true
	}
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(txsToProcess)))
	// Extract description hints.
	cache, extractedHintsCache, err := extractDescHints(ctx, database, descRegistry, counter, j.Source, j.Broker, txsToProcess)
	if err != nil {
		log.Printf("ingestion job %s: extract description hints: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	// Resolve instruments.
	instrumentIDs, idErrs, err := resolveInstruments(ctx, database, registry, j.Broker, j.Source, j.JobID, counter, txsToProcess, originalIndices, cache, extractedHintsCache)
	if err != nil {
		log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "instrument_description", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	if len(idErrs) > 0 {
		_ = database.AppendIdentificationErrors(ctx, j.JobID, idErrs)
	}
	// Store transactions.
	var storeErr error
	if j.Bulk {
		storeErr = database.ReplaceTxsInPeriod(ctx, j.UserID, j.Broker, j.PeriodFrom, j.PeriodTo, txsToProcess, instrumentIDs)
	} else {
		storeErr = database.CreateTx(ctx, j.UserID, j.Broker, txsToProcess[0].GetAccount(), txsToProcess[0], instrumentIDs[0])
	}
	if storeErr != nil {
		log.Printf("ingestion job %s: %v", j.JobID, storeErr)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: storeErr.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}

	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return true
}

// filterStoredTxs returns only txs with stored types and their original indices.
func filterStoredTxs(txs []*apiv1.Tx) ([]*apiv1.Tx, []int) {
	var filtered []*apiv1.Tx
	var indices []int
	for i, tx := range txs {
		if TxTypeStored(tx.Type) {
			filtered = append(filtered, tx)
			indices = append(indices, i)
		}
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
		r, err := Resolve(ctx, database, registry, broker, source, desc, HintsFromTx(tx), identifierHintsFromTx(ctx, tx), cache, rowIndex, counter, extractedHintsCache)
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
