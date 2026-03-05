package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListTxs lists transactions: by portfolio view (if portfolio_id set) or all user transactions. Filtering is via portfolios only.
func (s *Server) ListTxs(ctx context.Context, req *apiv1.ListTxsRequest) (*apiv1.ListTxsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var txs []*apiv1.PortfolioTx
	var nextToken string
	var err error
	if req.GetPortfolioId() != "" {
		ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		txs, nextToken, err = s.db.ListTxsByPortfolio(ctx, req.GetPortfolioId(), req.PeriodFrom, req.PeriodTo, pageSize, req.GetPageToken())
	} else {
		txs, nextToken, err = s.db.ListTxs(ctx, u.ID, nil, "", req.PeriodFrom, req.PeriodTo, pageSize, req.GetPageToken())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	underlyingIDs := make(map[string]struct{})
	for _, pt := range txs {
		if pt.GetTx().GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, pt.Tx.InstrumentId)
			if err == nil && inst != nil {
				pt.Instrument = instrumentRowToProto(inst)
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
		for _, pt := range txs {
			if pt.Instrument != nil && pt.Instrument.UnderlyingId != "" {
				if u := underlyingByID[pt.Instrument.UnderlyingId]; u != nil {
					pt.Instrument.Underlying = instrumentRowToProto(u)
				}
			}
		}
	}
	return &apiv1.ListTxsResponse{Txs: txs, NextPageToken: nextToken}, nil
}
