package postgres

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateJob_GetJob(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|j", "U", "u@u.com")
	from := timestamppb.Now()
	to := timestamppb.Now()
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID:     userID,
		JobType:    "tx",
		Broker:     "IBKR",
		Source:     "IBKR:test:statement",
		Filename:   "test.csv",
		PeriodFrom: from,
		PeriodTo:   to,
		Payload:    []byte("test-payload"),
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	status, errs, idErrs, jobUserID, totalCount, processedCount, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || len(errs) != 0 || len(idErrs) != 0 || jobUserID != userID {
		t.Fatalf("get job: %v %v %v %s", status, errs, idErrs, jobUserID)
	}
	if totalCount != 0 || processedCount != 0 {
		t.Fatalf("initial counts: total=%d processed=%d", totalCount, processedCount)
	}

	// Test LoadJobPayload.
	payload, err := p.LoadJobPayload(ctx, jobID)
	if err != nil {
		t.Fatalf("load payload: %v", err)
	}
	if string(payload) != "test-payload" {
		t.Fatalf("payload = %q, want test-payload", payload)
	}

	// Test ClearJobPayload.
	if err := p.ClearJobPayload(ctx, jobID); err != nil {
		t.Fatalf("clear payload: %v", err)
	}
	cleared, err := p.LoadJobPayload(ctx, jobID)
	if err != nil {
		t.Fatalf("load cleared payload: %v", err)
	}
	if cleared != nil {
		t.Fatalf("cleared payload = %v, want nil", cleared)
	}

	_ = p.SetJobStatus(ctx, jobID, apiv1.JobStatus_SUCCESS)
	_ = p.SetJobTotalCount(ctx, jobID, 5)
	_ = p.IncrJobProcessedCount(ctx, jobID)
	_ = p.IncrJobProcessedCount(ctx, jobID)
	_ = p.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	status2, errs2, idErrs2, _, totalCount2, processedCount2, _ := p.GetJob(ctx, jobID)
	if status2 != apiv1.JobStatus_SUCCESS || len(errs2) != 1 || len(idErrs2) != 0 {
		t.Fatalf("after update: %v %v %v", status2, errs2, idErrs2)
	}
	if totalCount2 != 5 || processedCount2 != 2 {
		t.Fatalf("after update counts: total=%d processed=%d", totalCount2, processedCount2)
	}
}

func TestCreateJob_PriceImport(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pi", "U", "u@pi.com")
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID:  userID,
		JobType: "price_import",
		Payload: []byte("price-data"),
	})
	if err != nil {
		t.Fatalf("create price import job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	status, _, _, jobUserID, _, _, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || jobUserID != userID {
		t.Fatalf("get job: status=%v user=%s", status, jobUserID)
	}
}

func TestListPendingJobs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pj", "U", "u@pj.com")
	j1, _ := p.CreateJob(ctx, db.CreateJobParams{
		UserID:  userID,
		JobType: "tx",
		Broker:  "IBKR",
		Source:  "IBKR:test:statement",
	})
	j2, _ := p.CreateJob(ctx, db.CreateJobParams{
		UserID:  userID,
		JobType: "price_import",
	})
	// Mark j1 as RUNNING (should still be returned).
	_ = p.SetJobStatus(ctx, j1, apiv1.JobStatus_RUNNING)

	jobs, err := p.ListPendingJobs(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d pending jobs, want 2", len(jobs))
	}
	// Order is non-deterministic when created_at is equal within a test transaction.
	byID := make(map[string]db.PendingJob)
	for _, j := range jobs {
		byID[j.ID] = j
	}
	if got, ok := byID[j1]; !ok || got.JobType != "tx" {
		t.Fatalf("j1 not found or wrong type: %+v", byID)
	}
	if got, ok := byID[j2]; !ok || got.JobType != "price_import" {
		t.Fatalf("j2 not found or wrong type: %+v", byID)
	}
}

func TestListJobs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|lj", "U", "u@lj.com")
	from := timestamppb.Now()
	to := timestamppb.Now()

	// Create two jobs.
	j1, _ := p.CreateJob(ctx, db.CreateJobParams{
		UserID:     userID,
		JobType:    "tx",
		Broker:     "IBKR",
		Source:     "IBKR:test:statement",
		Filename:   "file1.csv",
		PeriodFrom: from,
		PeriodTo:   to,
	})
	_, _ = p.CreateJob(ctx, db.CreateJobParams{
		UserID:     userID,
		JobType:    "tx",
		Broker:     "Fidelity",
		Source:     "Fidelity:web:fidelity-csv",
		Filename:   "file2.csv",
		PeriodFrom: from,
		PeriodTo:   to,
	})

	// Add errors to j1.
	_ = p.AppendValidationErrors(ctx, j1, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	_ = p.AppendIdentificationErrors(ctx, j1, []db.IdentificationError{{RowIndex: 1, InstrumentDescription: "AAPL", Message: "timeout"}})

	// List all (newest first).
	rows, total, nextToken, err := p.ListJobs(ctx, userID, 30, "")
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if total != 2 {
		t.Fatalf("got total %d, want 2", total)
	}
	if nextToken != "" {
		t.Fatalf("got next token %q, want empty", nextToken)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// Find j1 row (order is non-deterministic when timestamps are equal within a test transaction).
	var j1Row *db.JobRow
	for i := range rows {
		if rows[i].ID == j1 {
			j1Row = &rows[i]
		}
	}
	if j1Row == nil {
		t.Fatal("j1 not found in rows")
	}
	if j1Row.Filename != "file1.csv" {
		t.Fatalf("j1 filename %q, want file1.csv", j1Row.Filename)
	}
	if j1Row.ValidationErrorCount != 1 || j1Row.IdentificationErrorCount != 1 {
		t.Fatalf("j1 error counts: val=%d id=%d", j1Row.ValidationErrorCount, j1Row.IdentificationErrorCount)
	}

	// Pagination: page size 1.
	page1, _, tok1, _ := p.ListJobs(ctx, userID, 1, "")
	if len(page1) != 1 || tok1 == "" {
		t.Fatalf("page1: got %d rows, token %q", len(page1), tok1)
	}
	page2, _, tok2, _ := p.ListJobs(ctx, userID, 1, tok1)
	if len(page2) != 1 || tok2 != "" {
		t.Fatalf("page2: got %d rows, token %q", len(page2), tok2)
	}
}
