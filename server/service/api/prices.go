package api

import (
	"context"
	"time"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListPrices returns paginated EOD prices with optional search and filters. Admin only.
func (s *Server) ListPrices(ctx context.Context, req *apiv1.ListPricesRequest) (*apiv1.ListPricesResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}

	var dateFrom, dateTo time.Time
	if req.GetDateFrom() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateFrom())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid date_from: %v", err)
		}
		dateFrom = parsed
	}
	if req.GetDateTo() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateTo())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid date_to: %v", err)
		}
		dateTo = parsed
	}

	rows, totalCount, nextToken, err := s.db.ListPrices(ctx, req.GetSearch(), dateFrom, dateTo, req.GetDataProvider(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	prices := make([]*apiv1.EODPriceProto, 0, len(rows))
	for _, r := range rows {
		p := &apiv1.EODPriceProto{
			InstrumentId:          r.InstrumentID,
			InstrumentDisplayName: r.InstrumentDisplayName,
			PriceDate:             r.PriceDate.Format("2006-01-02"),
			Close:                 r.Close,
			DataProvider:          r.DataProvider,
			FetchedAt:             timestamppb.New(r.FetchedAt),
		}
		if r.Open != nil {
			p.Open = r.Open
		}
		if r.High != nil {
			p.High = r.High
		}
		if r.Low != nil {
			p.Low = r.Low
		}
		if r.AdjustedClose != nil {
			p.AdjustedClose = r.AdjustedClose
		}
		if r.Volume != nil {
			p.Volume = r.Volume
		}
		prices = append(prices, p)
	}

	return &apiv1.ListPricesResponse{
		Prices:        prices,
		NextPageToken: nextToken,
		TotalCount:    totalCount,
	}, nil
}
