package ingestion

import (
	"context"
	"log"
	"log/slog"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// ingestionLog is the logger for resolution and plugin orchestration (category server/service/ingestion).
// Set by RunWorker; when nil, resolve.go falls back to slog.Default().
var ingestionLog *slog.Logger

// RunWorker processes job requests from the channel until it is closed.
// Resolution uses DB, then in-batch cache, then description plugins (extract hints) and identifier plugins (timeout from config, retry once with backoff).
// ingestionLogger is the logger for ingestion/resolution (typically with category server/service/ingestion); may be nil.
// priceTrigger is optional; when non-nil, a non-blocking signal is sent after each successful job to trigger price fetching.
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, ingestionLogger *slog.Logger, priceTrigger chan<- struct{}) {
	ingestionLog = ingestionLogger
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			processJob(ctx, database, registry, descRegistry, counter, j, priceTrigger)
		}
	}
}

func processJob(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest, priceTrigger chan<- struct{}) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)
	var success bool
	if j.Bulk {
		success = processBulk(ctx, database, registry, descRegistry, counter, j)
	} else {
		success = processSingle(ctx, database, registry, descRegistry, counter, j)
	}
	if success {
		pricefetcher.Trigger(priceTrigger)
	}
}

func processBulk(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) bool {
	errs := ValidateTxs(j.Txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	// Drop txs that are not stored (e.g. SPLIT); decision is by TxType.
	var txsToProcess []*apiv1.Tx
	var originalIndices []int
	for i, tx := range j.Txs {
		if !TxTypeStored(tx.Type) {
			continue
		}
		txsToProcess = append(txsToProcess, tx)
		originalIndices = append(originalIndices, i)
	}
	_ = database.SetJobTotalCount(ctx, j.JobID, int32(len(txsToProcess)))
	cache := make(map[string]resolveResult)
	var extractedHintsCache map[string][]identifier.Identifier
	sourceDescriptionCheckedMiss := make(map[string]bool) // keys we looked up and got ""; Resolve will skip duplicate DB lookup
	// Collect distinct (source, description). Look up each in DB first; only run batch description extraction for those that miss (so re-uploads do not call OpenAI again).
	seen := make(map[string]bool)
	var batchItems []description.BatchItem
	idByKey := make(map[string]string)
	for _, tx := range txsToProcess {
		desc := tx.GetInstrumentDescription()
		key := cacheKey(j.Source, desc)
		if !seen[key] {
			seen[key] = true
			id, err := database.FindInstrumentBySourceDescription(ctx, j.Source, desc)
			if err != nil {
				log.Printf("ingestion job %s: find instrument by source description: %v", j.JobID, err)
				_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
					{RowIndex: -1, Field: "txs", Message: err.Error()},
				})
				_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
				return false
			}
			if id != "" {
				cache[key] = resolveResult{InstrumentID: id}
			} else {
				sourceDescriptionCheckedMiss[key] = true
				batchID := shortHashForBatch(key)
				idByKey[key] = batchID
				batchItems = append(batchItems, description.BatchItem{
					ID:                    batchID,
					InstrumentDescription: desc,
					Hints:                 HintsFromTx(tx),
				})
			}
		}
	}
	if len(batchItems) > 0 {
		hintsByID, err := runDescriptionPluginsBatch(ctx, database, descRegistry, counter, j.Broker, j.Source, batchItems)
		if err == nil && hintsByID != nil {
			extractedHintsCache = make(map[string][]identifier.Identifier)
			for key, id := range idByKey {
				extractedHintsCache[key] = hintsByID[id]
			}
		}
	}
	instrumentIDs := make([]string, len(txsToProcess))
	for i, tx := range txsToProcess {
		desc := tx.GetInstrumentDescription()
		rowIndex := int32(originalIndices[i])
		r, err := Resolve(ctx, database, registry, descRegistry, j.Broker, j.Source, desc, HintsFromTx(tx), identifierHintsFromTx(ctx, tx), cache, rowIndex, counter, extractedHintsCache, sourceDescriptionCheckedMiss)
		if err != nil {
			log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
			_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
				{RowIndex: rowIndex, Field: "instrument_description", Message: err.Error()},
			})
			_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
			return false
		}
		instrumentIDs[i] = r.InstrumentID
		_ = database.IncrJobProcessedCount(ctx, j.JobID)
	}
	var idErrs []db.IdentificationError
	for _, r := range cache {
		if r.IdErr != nil {
			idErrs = append(idErrs, *r.IdErr)
		}
	}
	if len(idErrs) > 0 {
		_ = database.AppendIdentificationErrors(ctx, j.JobID, idErrs)
	}
	err := database.ReplaceTxsInPeriod(ctx, j.UserID, j.Broker, j.PeriodFrom, j.PeriodTo, txsToProcess, instrumentIDs)
	if err != nil {
		log.Printf("ingestion job %s: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return true
}

func processSingle(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) bool {
	errs := ValidateTx(j.Tx, 0)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	if !TxTypeStored(j.Tx.Type) {
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
		return true
	}
	_ = database.SetJobTotalCount(ctx, j.JobID, 1)
	desc := j.Tx.GetInstrumentDescription()
	r, err := Resolve(ctx, database, registry, descRegistry, j.Broker, j.Source, desc, HintsFromTx(j.Tx), identifierHintsFromTx(ctx, j.Tx), nil, 0, counter, nil, nil)
	if err != nil {
		log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: 0, Field: "instrument_description", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	_ = database.IncrJobProcessedCount(ctx, j.JobID)
	if r.IdErr != nil {
		_ = database.AppendIdentificationErrors(ctx, j.JobID, []db.IdentificationError{*r.IdErr})
	}
	err = database.CreateTx(ctx, j.UserID, j.Broker, j.Tx.GetAccount(), j.Tx, r.InstrumentID)
	if err != nil {
		log.Printf("ingestion job %s: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: 0, Field: "tx", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return false
	}
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
	return true
}
