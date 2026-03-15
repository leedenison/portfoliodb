package api

import (
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetJob_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", int32(0), int32(0), nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestGetJob_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_PENDING, nil, nil, "user-1", int32(0), int32(0), nil)
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
		{ID: "j2", Filename: "", Broker: "Fidelity", Status: "FAILED", CreatedAt: now.Add(-time.Hour), ValidationErrorCount: 3, IdentificationErrorCount: 0},
	}
	mockDB.EXPECT().
		ListJobs(gomock.Any(), "user-1", int32(30), "").
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
		ListJobs(gomock.Any(), "user-1", int32(100), "").
		Return(nil, int32(0), "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{PageSize: 999})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}
