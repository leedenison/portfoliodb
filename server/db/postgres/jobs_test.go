package postgres

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateJob_GetJob(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|j", "U", "u@u.com")
	from := timestamppb.Now()
	before := timestamppb.Now()
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID:       userID,
		JobType:      "tx",
		Broker:       "IBKR",
		Source:       "IBKR:test:statement",
		Filename:     "test.csv",
		PeriodFrom:   from,
		PeriodBefore: before,
		Payload:      []byte("test-payload"),
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	d, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if d.Status != apiv1.JobStatus_PENDING || len(d.ValidationErrors) != 0 || len(d.IdentificationErrors) != 0 || d.UserID != userID {
		t.Fatalf("get job: %v %v %v %s", d.Status, d.ValidationErrors, d.IdentificationErrors, d.UserID)
	}
	if d.TotalCount != 0 || d.ProcessedCount != 0 {
		t.Fatalf("initial counts: total=%d processed=%d", d.TotalCount, d.ProcessedCount)
	}
	if len(d.Parts) != 0 {
		t.Fatalf("a tx job has no parts, got %d", len(d.Parts))
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
	_ = p.AppendValidationErrors(ctx, jobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	d2, _ := p.GetJob(ctx, jobID)
	if d2.Status != apiv1.JobStatus_SUCCESS || len(d2.ValidationErrors) != 1 || len(d2.IdentificationErrors) != 0 {
		t.Fatalf("after update: %v %v %v", d2.Status, d2.ValidationErrors, d2.IdentificationErrors)
	}
	if d2.TotalCount != 5 || d2.ProcessedCount != 2 {
		t.Fatalf("after update counts: total=%d processed=%d", d2.TotalCount, d2.ProcessedCount)
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
		JobType: db.JobTypeSystemArchive,
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
	if got, ok := byID[j2]; !ok || got.JobType != db.JobTypeSystemArchive {
		t.Fatalf("j2 not found or wrong type: %+v", byID)
	}
}

func TestListJobs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|lj", "U", "u@lj.com")
	from := timestamppb.Now()
	before := timestamppb.Now()

	// Create two jobs.
	j1, _ := p.CreateJob(ctx, db.CreateJobParams{
		UserID:       userID,
		JobType:      "tx",
		Broker:       "IBKR",
		Source:       "IBKR:test:statement",
		Filename:     "file1.csv",
		PeriodFrom:   from,
		PeriodBefore: before,
	})
	_, _ = p.CreateJob(ctx, db.CreateJobParams{
		UserID:       userID,
		JobType:      "tx",
		Broker:       "FIDELITY",
		Source:       "Fidelity:web:fidelity-csv",
		Filename:     "file2.csv",
		PeriodFrom:   from,
		PeriodBefore: before,
	})

	// Add errors to j1.
	_ = p.AppendValidationErrors(ctx, j1, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	_ = p.AppendIdentificationErrors(ctx, j1, []db.IdentificationError{{RowIndex: 1, InstrumentDescription: "AAPL", Message: "timeout"}})

	// List all (newest first).
	rows, total, nextToken, err := p.ListJobs(ctx, userID, "", 30, "")
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
	page1, _, tok1, _ := p.ListJobs(ctx, userID, "", 1, "")
	if len(page1) != 1 || tok1 == "" {
		t.Fatalf("page1: got %d rows, token %q", len(page1), tok1)
	}
	page2, _, tok2, _ := p.ListJobs(ctx, userID, "", 1, tok1)
	if len(page2) != 1 || tok2 != "" {
		t.Fatalf("page2: got %d rows, token %q", len(page2), tok2)
	}
}

// A system archive job's part rows exist from creation, so a caller polling
// before the worker starts sees the parts it will apply rather than an empty
// list it cannot tell apart from an archive that carried nothing.
func TestCreateJob_SystemArchiveParts(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|sa", "U", "u@sa.com")
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID:  userID,
		JobType: db.JobTypeSystemArchive,
		Payload: []byte("archive"),
		// Deliberately out of restore order: GetJob sorts by the enum, not by
		// the order the caller happened to list them in.
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_CORPORATE_EVENTS, archivev1.ArchivePart_INSTRUMENTS},
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	d, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if len(d.Parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(d.Parts))
	}
	if d.Parts[0].Part != archivev1.ArchivePart_INSTRUMENTS || d.Parts[1].Part != archivev1.ArchivePart_CORPORATE_EVENTS {
		t.Fatalf("parts out of restore order: %v, %v", d.Parts[0].Part, d.Parts[1].Part)
	}
	for _, r := range d.Parts {
		if r.Status != apiv1.JobStatus_PENDING || r.TotalCount != 0 || r.ProcessedCount != 0 {
			t.Fatalf("part %v starts as %v %d/%d", r.Part, r.Status, r.ProcessedCount, r.TotalCount)
		}
	}
}

func TestJobParts_ProgressStatusAndErrors(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|sap", "U", "u@sap.com")
	jobID, _ := p.CreateJob(ctx, db.CreateJobParams{
		UserID:  userID,
		JobType: db.JobTypeSystemArchive,
		Parts:   []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_PRICES},
	})

	if err := p.SetJobPartStatus(ctx, jobID, archivev1.ArchivePart_INSTRUMENTS, apiv1.JobStatus_SUCCESS); err != nil {
		t.Fatalf("set part status: %v", err)
	}
	if err := p.SetJobPartTotalCount(ctx, jobID, archivev1.ArchivePart_PRICES, 10); err != nil {
		t.Fatalf("set part total: %v", err)
	}
	// Batched progress: the delta is the point, so two calls must add up.
	_ = p.AddJobPartProcessedCount(ctx, jobID, archivev1.ArchivePart_PRICES, 4)
	_ = p.AddJobPartProcessedCount(ctx, jobID, archivev1.ArchivePart_PRICES, 3)
	if err := p.SetJobPartFailed(ctx, jobID, archivev1.ArchivePart_PRICES, "upsert failed"); err != nil {
		t.Fatalf("set part failed: %v", err)
	}
	_ = p.AppendValidationErrors(ctx, jobID, archivev1.ArchivePart_PRICES,
		[]*apiv1.ValidationError{{RowIndex: 2, Field: "close", Message: "unparseable"}})
	// An error with no part belongs to the job rather than to any one part.
	_ = p.AppendValidationErrors(ctx, jobID, archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED,
		[]*apiv1.ValidationError{{RowIndex: -1, Field: "payload", Message: "unreadable"}})

	d, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if len(d.ValidationErrors) != 1 || d.ValidationErrors[0].GetField() != "payload" {
		t.Fatalf("job-level errors = %v", d.ValidationErrors)
	}
	instruments, prices := d.Parts[0], d.Parts[1]
	if instruments.Status != apiv1.JobStatus_SUCCESS || len(instruments.ValidationErrors) != 0 {
		t.Fatalf("instruments = %v with %d errors", instruments.Status, len(instruments.ValidationErrors))
	}
	if prices.Status != apiv1.JobStatus_FAILED || prices.Message != "upsert failed" {
		t.Fatalf("prices = %v %q", prices.Status, prices.Message)
	}
	if prices.TotalCount != 10 || prices.ProcessedCount != 7 {
		t.Fatalf("prices progress = %d/%d", prices.ProcessedCount, prices.TotalCount)
	}
	if len(prices.ValidationErrors) != 1 || prices.ValidationErrors[0].GetField() != "close" {
		t.Fatalf("prices errors = %v", prices.ValidationErrors)
	}

	// A re-run after a restart starts the part's progress over.
	if err := p.ResetJobPartProgress(ctx, jobID, archivev1.ArchivePart_PRICES); err != nil {
		t.Fatalf("reset part progress: %v", err)
	}
	d2, _ := p.GetJob(ctx, jobID)
	if d2.Parts[1].ProcessedCount != 0 || d2.Parts[1].Message != "" {
		t.Fatalf("after reset: %d processed, message %q", d2.Parts[1].ProcessedCount, d2.Parts[1].Message)
	}
}

func TestListJobs_FiltersByJobType(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|jt", "U", "u@jt.com")
	_, _ = p.CreateJob(ctx, db.CreateJobParams{UserID: userID, JobType: db.JobTypeTx})
	archiveJob, _ := p.CreateJob(ctx, db.CreateJobParams{UserID: userID, JobType: db.JobTypeSystemArchive})

	all, allTotal, _, err := p.ListJobs(ctx, userID, "", 30, "")
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(all) != 2 || allTotal != 2 {
		t.Fatalf("unfiltered = %d rows, total %d", len(all), allTotal)
	}
	filtered, total, _, err := p.ListJobs(ctx, userID, db.JobTypeSystemArchive, 30, "")
	if err != nil {
		t.Fatalf("list jobs filtered: %v", err)
	}
	if len(filtered) != 1 || total != 1 || filtered[0].ID != archiveJob {
		t.Fatalf("filtered = %d rows, total %d", len(filtered), total)
	}
}
