package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var jobStatusFromStr = map[string]apiv1.JobStatus{
	"PENDING": apiv1.JobStatus_PENDING,
	"RUNNING": apiv1.JobStatus_RUNNING,
	"SUCCESS": apiv1.JobStatus_SUCCESS,
	"FAILED":  apiv1.JobStatus_FAILED,
}

// GetJob returns ingestion job status and validation errors; job must belong to user.
func (s *Server) GetJob(ctx context.Context, req *apiv1.GetJobRequest) (*apiv1.GetJobResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id required")
	}
	d, err := s.db.GetJob(ctx, req.GetJobId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// A job that does not exist and one belonging to someone else are the same
	// answer: revealing the difference would confirm the id.
	if d.UserID == "" || d.UserID != u.ID {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	idErrProtos := make([]*apiv1.IdentificationError, 0, len(d.IdentificationErrors))
	for _, e := range d.IdentificationErrors {
		idErrProtos = append(idErrProtos, &apiv1.IdentificationError{
			RowIndex:              e.RowIndex,
			InstrumentDescription: e.InstrumentDescription,
			Message:               e.Message,
		})
	}
	parts := make([]*apiv1.JobPartResult, 0, len(d.Parts))
	for _, r := range d.Parts {
		parts = append(parts, &apiv1.JobPartResult{
			Part:             r.Part,
			Status:           r.Status,
			TotalCount:       r.TotalCount,
			ProcessedCount:   r.ProcessedCount,
			ValidationErrors: r.ValidationErrors,
			Message:          r.Message,
		})
	}
	return &apiv1.GetJobResponse{
		Status:               d.Status,
		ValidationErrors:     d.ValidationErrors,
		IdentificationErrors: idErrProtos,
		TotalCount:           d.TotalCount,
		ProcessedCount:       d.ProcessedCount,
		Parts:                parts,
	}, nil
}

// ListJobs returns paginated ingestion jobs for the authenticated user, newest
// first. An empty job_type matches every type.
func (s *Server) ListJobs(ctx context.Context, req *apiv1.ListJobsRequest) (*apiv1.ListJobsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}
	rows, totalCount, nextToken, err := s.db.ListJobs(ctx, u.ID, req.GetJobType(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	jobs := make([]*apiv1.Job, 0, len(rows))
	for _, r := range rows {
		jobs = append(jobs, &apiv1.Job{
			Id:                       r.ID,
			Filename:                 r.Filename,
			Broker:                   r.Broker,
			Status:                   jobStatusFromStr[r.Status],
			CreatedAt:                timestamppb.New(r.CreatedAt),
			ValidationErrorCount:     r.ValidationErrorCount,
			IdentificationErrorCount: r.IdentificationErrorCount,
			JobType:                  r.JobType,
		})
	}
	return &apiv1.ListJobsResponse{Jobs: jobs, NextPageToken: nextToken, TotalCount: totalCount}, nil
}
