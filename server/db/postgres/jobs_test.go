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
	jobID, err := p.CreateJob(ctx, userID, "IBKR", "IBKR:test:statement", "test.csv", from, to)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	status, errs, idErrs, jobUserID, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || len(errs) != 0 || len(idErrs) != 0 || jobUserID != userID {
		t.Fatalf("get job: %v %v %v %s", status, errs, idErrs, jobUserID)
	}
	_ = p.SetJobStatus(ctx, jobID, apiv1.JobStatus_SUCCESS)
	_ = p.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	status2, errs2, idErrs2, _, _ := p.GetJob(ctx, jobID)
	if status2 != apiv1.JobStatus_SUCCESS || len(errs2) != 1 || len(idErrs2) != 0 {
		t.Fatalf("after update: %v %v %v", status2, errs2, idErrs2)
	}
}

func TestListJobs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|lj", "U", "u@lj.com")
	from := timestamppb.Now()
	to := timestamppb.Now()

	// Create two jobs.
	j1, _ := p.CreateJob(ctx, userID, "IBKR", "IBKR:test:statement", "file1.csv", from, to)
	_, _ = p.CreateJob(ctx, userID, "Fidelity", "Fidelity:web:fidelity-csv", "file2.csv", from, to)

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
