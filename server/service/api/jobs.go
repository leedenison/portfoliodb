package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetJob returns ingestion job status and validation errors; job must belong to user.
func (s *Server) GetJob(ctx context.Context, req *apiv1.GetJobRequest) (*apiv1.GetJobResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id required")
	}
	statusVal, errs, idErrs, jobUserID, err := s.db.GetJob(ctx, req.GetJobId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if jobUserID == "" {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	if jobUserID != u.ID {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	idErrProtos := make([]*apiv1.IdentificationError, 0, len(idErrs))
	for _, e := range idErrs {
		idErrProtos = append(idErrProtos, &apiv1.IdentificationError{
			RowIndex:               e.RowIndex,
			InstrumentDescription: e.InstrumentDescription,
			Message:                e.Message,
		})
	}
	return &apiv1.GetJobResponse{Status: statusVal, ValidationErrors: errs, IdentificationErrors: idErrProtos}, nil
}
