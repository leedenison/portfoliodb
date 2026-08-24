package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
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
		// Not `:=`: that would shadow err, and the ComputeHoldingsForPortfolio
		// error below would never reach the check after the block.
		var ok bool
		ok, err = s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
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
	// Batch-load instruments for enrichment
	instIDs := make([]string, 0, len(holdings))
	for _, h := range holdings {
		if h.GetInstrumentId() != "" {
			instIDs = append(instIDs, h.InstrumentId)
		}
	}
	if len(instIDs) > 0 {
		instRows, err := s.db.ListInstrumentsByIDs(ctx, instIDs)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		instByID := make(map[string]*db.InstrumentRow, len(instRows))
		underlyingIDs := make([]string, 0)
		for _, r := range instRows {
			instByID[r.ID] = r
			if r.UnderlyingID != nil && *r.UnderlyingID != "" {
				underlyingIDs = append(underlyingIDs, *r.UnderlyingID)
			}
		}
		for _, h := range holdings {
			if inst := instByID[h.GetInstrumentId()]; inst != nil {
				h.Instrument = instrumentRowToProto(inst)
			}
		}
		// Batch-load underlyings
		if len(underlyingIDs) > 0 {
			underlyingRows, err := s.db.ListInstrumentsByIDs(ctx, underlyingIDs)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			underlyingByID := make(map[string]*db.InstrumentRow, len(underlyingRows))
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
	}
	return &apiv1.GetHoldingsResponse{Holdings: holdings, AsOf: asOf}, nil
}

// CountUnattributedHoldings returns the dashboard headline counts for positions
// on no currency line. Admin only: an unattributed holding is a gap in the shared
// instrument data rather than a fact about one portfolio, and the repair is made
// where that data is.
func (s *Server) CountUnattributedHoldings(ctx context.Context, req *apiv1.CountUnattributedHoldingsRequest) (*apiv1.CountUnattributedHoldingsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	noLineNamed, noCurrencyKnown, err := s.db.CountUnattributedHoldings(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count unattributed holdings: %v", err)
	}
	return &apiv1.CountUnattributedHoldingsResponse{
		NoLineNamedCount:     noLineNamed,
		NoCurrencyKnownCount: noCurrencyKnown,
	}, nil
}
