package postgres

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateJob_GetJob(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|j", "U", "u@u.com")
	from := timestamppb.Now()
	to := timestamppb.Now()
	jobID, err := p.CreateJob(ctx, userID, "IBKR", "IBKR:test:statement", from, to)
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
