package api

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListTxs lists transactions: by portfolio view (if portfolio_id set) or all user transactions.
// Portfolios are the general filtering mechanism; the broker filter and sort direction exist so a
// client can fetch the most recent transaction for one broker in a single request.
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
	var broker *typev1.Broker
	if b := req.GetBroker(); b != typev1.Broker_BROKER_UNSPECIFIED {
		broker = &b
	}
	var txs []*apiv1.PortfolioTx
	var nextToken string
	var err error
	if req.GetPortfolioId() != "" {
		var ok bool
		ok, err = s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		txs, nextToken, err = s.db.ListTxsByPortfolio(ctx, req.GetPortfolioId(), broker, req.PeriodFrom, req.PeriodBefore, req.GetDescending(), pageSize, req.GetPageToken())
	} else {
		txs, nextToken, err = s.db.ListTxs(ctx, u.ID, broker, "", req.PeriodFrom, req.PeriodBefore, req.GetDescending(), pageSize, req.GetPageToken())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Batch-load instruments for enrichment
	instIDs := make([]string, 0, len(txs))
	for _, pt := range txs {
		if pt.GetTx().GetInstrumentId() != "" {
			instIDs = append(instIDs, pt.Tx.InstrumentId)
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
		for _, pt := range txs {
			if inst := instByID[pt.GetTx().GetInstrumentId()]; inst != nil {
				pt.Instrument = instrumentRowToProto(inst)
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
			for _, pt := range txs {
				if pt.Instrument != nil && pt.Instrument.UnderlyingId != "" {
					if u := underlyingByID[pt.Instrument.UnderlyingId]; u != nil {
						pt.Instrument.Underlying = instrumentRowToProto(u)
					}
				}
			}
		}
	}
	return &apiv1.ListTxsResponse{Txs: txs, NextPageToken: nextToken}, nil
}

// txTypeForAssetClass returns the OFX tx type for a given asset class and quantity sign.
func txTypeForAssetClass(assetClass string, qty decimal.Decimal) string {
	buy, sell := "BUYOTHER", "SELLOTHER"
	switch assetClass {
	case "STOCK", "ETF":
		buy, sell = "BUYSTOCK", "SELLSTOCK"
	case "OPTION":
		buy, sell = "BUYOPT", "SELLOPT"
	case "MUTUAL_FUND":
		buy, sell = "BUYMF", "SELLMF"
	case "FIXED_INCOME":
		buy, sell = "BUYDEBT", "SELLDEBT"
	case "CASH":
		buy, sell = "JRNLFUND", "JRNLFUND"
	}
	if qty.IsNegative() {
		return sell
	}
	return buy
}
