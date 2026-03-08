package ingestion

import (
	"context"
	"log"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// RunWorker processes job requests from the channel until it is closed.
// Resolution uses DB, then in-batch cache, then description plugins (extract hints) and identifier plugins (timeout from config, retry once with backoff).
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			processJob(ctx, database, registry, descRegistry, counter, j)
		}
	}
}

func processJob(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)
	if j.Bulk {
		processBulk(ctx, database, registry, descRegistry, counter, j)
	} else {
		processSingle(ctx, database, registry, descRegistry, counter, j)
	}
}

func processBulk(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) {
	errs := ValidateTxs(j.Txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	cache := make(map[string]resolveResult)
	var extractedHintsCache map[string][]identifier.Identifier
	sourceDescriptionCheckedMiss := make(map[string]bool) // keys we looked up and got ""; Resolve will skip duplicate DB lookup
	// Collect distinct (source, description). Look up each in DB first; only run batch description extraction for those that miss (so re-uploads do not call OpenAI again).
	seen := make(map[string]bool)
	var batchItems []description.BatchItem
	idByKey := make(map[string]string)
	for _, tx := range j.Txs {
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
				return
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
	instrumentIDs := make([]string, len(j.Txs))
	for i, tx := range j.Txs {
		desc := tx.GetInstrumentDescription()
		r, err := Resolve(ctx, database, registry, descRegistry, j.Broker, j.Source, desc, HintsFromTx(tx), identifierHintsFromTx(ctx, tx), cache, int32(i), counter, extractedHintsCache, sourceDescriptionCheckedMiss)
		if err != nil {
			log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
			_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
				{RowIndex: int32(i), Field: "instrument_description", Message: err.Error()},
			})
			_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
			return
		}
		instrumentIDs[i] = r.InstrumentID
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
	err := database.ReplaceTxsInPeriod(ctx, j.UserID, j.Broker, j.PeriodFrom, j.PeriodTo, j.Txs, instrumentIDs)
	if err != nil {
		log.Printf("ingestion job %s: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: -1, Field: "txs", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
}

func processSingle(ctx context.Context, database db.DB, registry *identifier.Registry, descRegistry *description.Registry, counter telemetry.CounterIncrementer, j *JobRequest) {
	errs := ValidateTx(j.Tx, 0)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	desc := j.Tx.GetInstrumentDescription()
	r, err := Resolve(ctx, database, registry, descRegistry, j.Broker, j.Source, desc, HintsFromTx(j.Tx), identifierHintsFromTx(ctx, j.Tx), nil, 0, counter, nil, nil)
	if err != nil {
		log.Printf("ingestion job %s: resolve instrument: %v", j.JobID, err)
		_ = database.AppendValidationErrors(ctx, j.JobID, []*apiv1.ValidationError{
			{RowIndex: 0, Field: "instrument_description", Message: err.Error()},
		})
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
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
		return
	}
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_SUCCESS)
}
