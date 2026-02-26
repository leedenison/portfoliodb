package ingestion

import (
	"context"
	"log"

	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

// RunWorker processes job requests from the channel until it is closed.
func RunWorker(ctx context.Context, database db.DB, queue <-chan *JobRequest) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-queue:
			if !ok {
				return
			}
			processJob(ctx, database, j)
		}
	}
}

func processJob(ctx context.Context, database db.DB, j *JobRequest) {
	_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_RUNNING)
	if j.Bulk {
		processBulk(ctx, database, j)
	} else {
		processSingle(ctx, database, j)
	}
}

func processBulk(ctx context.Context, database db.DB, j *JobRequest) {
	errs := ValidateTxs(j.Txs)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	err := database.ReplaceTxsInPeriod(ctx, j.PortfolioID, j.Broker, j.PeriodFrom, j.PeriodTo, j.Txs)
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

func processSingle(ctx context.Context, database db.DB, j *JobRequest) {
	errs := ValidateTx(j.Tx, 0)
	if len(errs) > 0 {
		_ = database.AppendValidationErrors(ctx, j.JobID, errs)
		_ = database.SetJobStatus(ctx, j.JobID, apiv1.JobStatus_FAILED)
		return
	}
	err := database.UpsertTx(ctx, j.PortfolioID, j.Broker, j.Tx)
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
