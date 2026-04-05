package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListInflationIndices returns paginated monthly inflation indices with optional filters. Admin only.
func (s *Server) ListInflationIndices(ctx context.Context, req *apiv1.ListInflationIndicesRequest) (*apiv1.ListInflationIndicesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}

	dateFrom := dateToTimePtr(req.GetDateFrom())
	dateTo := dateToTimePtr(req.GetDateTo())

	rows, nextToken, totalCount, err := s.db.ListInflationIndices(ctx, req.GetCurrency(), dateFrom, dateTo, pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	indices := make([]*apiv1.InflationIndexProto, 0, len(rows))
	for _, r := range rows {
		indices = append(indices, &apiv1.InflationIndexProto{
			Currency:     r.Currency,
			Month:        r.Month.Format("2006-01-02"),
			IndexValue:   r.IndexValue,
			BaseYear:     int32(r.BaseYear),
			DataProvider: r.DataProvider,
		})
	}

	return &apiv1.ListInflationIndicesResponse{
		Indices:       indices,
		NextPageToken: nextToken,
		TotalCount:    int32(totalCount),
	}, nil
}
