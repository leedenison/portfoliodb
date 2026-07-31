package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetPortfolioValuation returns daily portfolio values over the half-open
// [date_from, date_before) range. An empty range returns no points.
func (s *Server) GetPortfolioValuation(ctx context.Context, req *apiv1.GetPortfolioValuationRequest) (*apiv1.GetPortfolioValuationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}

	dateFrom := dateToTime(req.GetDateFrom())
	dateBefore := dateToTime(req.GetDateBefore())
	if dateBefore.Before(dateFrom) {
		return nil, status.Error(codes.InvalidArgument, "date_before must not be before date_from")
	}

	displayCurrency := req.GetDisplayCurrency()
	if displayCurrency == "" {
		dc, dcErr := s.db.GetDisplayCurrency(ctx, u.ID)
		if dcErr != nil {
			return nil, status.Error(codes.Internal, dcErr.Error())
		}
		displayCurrency = dc
	}

	var points []db.ValuationPoint
	if req.GetPortfolioId() != "" {
		ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		points, err = s.db.GetPortfolioValuation(ctx, req.GetPortfolioId(), dateFrom, dateBefore, displayCurrency)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		var err error
		points, err = s.db.GetUserValuation(ctx, u.ID, dateFrom, dateBefore, displayCurrency)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	resp := &apiv1.GetPortfolioValuationResponse{
		Points: make([]*apiv1.ValuationPoint, len(points)),
	}
	for i, pt := range points {
		resp.Points[i] = &apiv1.ValuationPoint{
			Date:                pt.Date.Format("2006-01-02"),
			TotalValue:          pt.TotalValue,
			UnpricedInstruments: pt.UnpricedInstruments,
		}
	}
	return resp, nil
}
