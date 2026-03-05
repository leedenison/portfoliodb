package api

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetJob_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestGetJob_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_PENDING, nil, nil, "user-1", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if resp.GetStatus() != apiv1.JobStatus_PENDING {
		t.Fatalf("got status %v", resp.GetStatus())
	}
}
