package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetHoldings returns holdings: by portfolio view (if portfolio_id set) or all user holdings. Filtering is via portfolios only.
func (s *Server) GetHoldings(ctx context.Context, req *apiv1.GetHoldingsRequest) (*apiv1.GetHoldingsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	var holdings []*apiv1.Holding
	var asOf *timestamppb.Timestamp
	var err error
	if req.GetPortfolioId() != "" {
		ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		holdings, asOf, err = s.db.ComputeHoldingsForPortfolio(ctx, req.GetPortfolioId(), req.AsOf)
	} else {
		holdings, asOf, err = s.db.ComputeHoldings(ctx, u.ID, nil, "", req.AsOf)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	underlyingIDs := make(map[string]struct{})
	for _, h := range holdings {
		if h.GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, h.InstrumentId)
			if err == nil && inst != nil {
				h.Instrument = instrumentRowToProto(inst)
				if inst.UnderlyingID != "" {
					underlyingIDs[inst.UnderlyingID] = struct{}{}
				}
			}
		}
	}
	// Single batch query for all underlyings
	if len(underlyingIDs) > 0 {
		ids := make([]string, 0, len(underlyingIDs))
		for id := range underlyingIDs {
			ids = append(ids, id)
		}
		underlyingRows, err := s.db.ListInstrumentsByIDs(ctx, ids)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		underlyingByID := make(map[string]*db.InstrumentRow)
		for _, r := range underlyingRows {
			underlyingByID[r.ID] = r
		}
		for _, h := range holdings {
			if h.Instrument != nil && h.Instrument.UnderlyingId != "" {
				if u := underlyingByID[h.Instrument.UnderlyingId]; u != nil {
					h.Instrument.Underlying = instrumentRowToProto(u)
				}
			}
		}
	}
	return &apiv1.GetHoldingsResponse{Holdings: holdings, AsOf: asOf}, nil
}
