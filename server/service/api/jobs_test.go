package api

import (
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetJob_NotFound(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(&db.JobDetail{}, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestGetJob_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(&db.JobDetail{Status: apiv1.JobStatus_PENDING, UserID: "user-1"}, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if resp.GetStatus() != apiv1.JobStatus_PENDING {
		t.Fatalf("got status %v", resp.GetStatus())
	}
}

func TestListJobs_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	now := time.Now()
	rows := []db.JobRow{
		{ID: "j1", Filename: "test.csv", Broker: "IBKR", Status: "SUCCESS", CreatedAt: now, ValidationErrorCount: 0, IdentificationErrorCount: 2},
		{ID: "j2", Filename: "", Broker: "FIDELITY", Status: "FAILED", CreatedAt: now.Add(-time.Hour), ValidationErrorCount: 3, IdentificationErrorCount: 0},
	}
	mockDB.EXPECT().
		ListJobs(gomock.Any(), "user-1", "", int32(30), "").
		Return(rows, int32(2), "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(resp.GetJobs()) != 2 {
		t.Fatalf("got %d jobs, want 2", len(resp.GetJobs()))
	}
	if resp.GetJobs()[0].GetFilename() != "test.csv" {
		t.Fatalf("got filename %q", resp.GetJobs()[0].GetFilename())
	}
	if resp.GetJobs()[0].GetStatus() != apiv1.JobStatus_SUCCESS {
		t.Fatalf("got status %v", resp.GetJobs()[0].GetStatus())
	}
	if resp.GetJobs()[0].GetIdentificationErrorCount() != 2 {
		t.Fatalf("got id error count %d", resp.GetJobs()[0].GetIdentificationErrorCount())
	}
	if resp.GetTotalCount() != 2 {
		t.Fatalf("got total %d", resp.GetTotalCount())
	}
}

func TestListJobs_PageSizeClamping(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		ListJobs(gomock.Any(), "user-1", "", int32(100), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{PageSize: 999})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}

// A system archive job reports a row per part, so the page can render the parts
// it will apply before the worker has touched any of them.
func TestGetJob_ReturnsPerPartResults(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(&db.JobDetail{
			Status: apiv1.JobStatus_RUNNING,
			UserID: "user-1",
			Parts: []db.JobPartResult{
				{Part: archivev1.ArchivePart_INSTRUMENTS, Status: apiv1.JobStatus_SUCCESS, TotalCount: 3, ProcessedCount: 3},
				{Part: archivev1.ArchivePart_PRICES, Status: apiv1.JobStatus_RUNNING, TotalCount: 9, ProcessedCount: 4,
					ValidationErrors: []*apiv1.ValidationError{{RowIndex: 1, Field: "close", Message: "bad"}}},
			},
		}, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	parts := resp.GetParts()
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if parts[0].GetPart() != archivev1.ArchivePart_INSTRUMENTS || parts[0].GetStatus() != apiv1.JobStatus_SUCCESS {
		t.Fatalf("part 0 = %v %v", parts[0].GetPart(), parts[0].GetStatus())
	}
	if parts[1].GetProcessedCount() != 4 || len(parts[1].GetValidationErrors()) != 1 {
		t.Fatalf("part 1 = %d processed, %d errors", parts[1].GetProcessedCount(), len(parts[1].GetValidationErrors()))
	}
}

func TestListJobs_ForwardsJobTypeFilter(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		ListJobs(gomock.Any(), "user-1", db.JobTypeSystemArchive, int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	if _, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{JobType: db.JobTypeSystemArchive}); err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}
