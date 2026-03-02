package ingestion

import (
	"context"
	"log"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// RunWorker processes job requests from the channel until it is closed.
// Resolution uses DB, then in-batch cache, then enabled plugins from registry (timeout from config, retry once with backoff).
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest, registry *identifier.Registry) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			processJob(ctx, database, registry, j)
		}
	}
}

func processJob(ctx context.Context, database db.DB, registry *identifier.Registry, j *JobRequest) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)
	if j.Bulk {
		processBulk(ctx, database, registry, j)
	} else {
		processSingle(ctx, database, registry, j)
	}
}

func processBulk(ctx context.Context, database db.DB, registry *identifier.Registry, j *JobRequest) {
	errs := ValidateTxs(j.Txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	cache := make(map[string]resolveResult)
	instrumentIDs := make([]string, len(j.Txs))
	for i, tx := range j.Txs {
		desc := tx.GetInstrumentDescription()
		r, err := Resolve(ctx, database, registry, j.Broker, j.Source, desc, cache, int32(i))
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

func processSingle(ctx context.Context, database db.DB, registry *identifier.Registry, j *JobRequest) {
	errs := ValidateTx(j.Tx, 0)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	desc := j.Tx.GetInstrumentDescription()
	r, err := Resolve(ctx, database, registry, j.Broker, j.Source, desc, nil, 0)
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
