package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListPriceGaps returns date ranges where prices are needed but not cached. Admin only.
func (s *Server) ListPriceGaps(ctx context.Context, req *apiv1.ListPriceGapsRequest) (*apiv1.ListPriceGapsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}

	opts := db.HeldRangesOpts{ExtendToToday: true}

	priceGaps, err := s.db.PriceGaps(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "price gaps: %v", err)
	}
	fxGaps, err := s.db.FXGaps(ctx, opts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fx gaps: %v", err)
	}

	// Collect all instrument IDs for metadata lookup.
	idSet := make(map[string]bool, len(priceGaps)+len(fxGaps))
	for _, g := range priceGaps {
		idSet[g.InstrumentID] = true
	}
	for _, g := range fxGaps {
		idSet[g.InstrumentID] = true
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	instruments, err := s.db.ListInstrumentsByIDs(ctx, ids)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list instruments: %v", err)
	}
	instrMap := make(map[string]*db.InstrumentRow, len(instruments))
	for _, inst := range instruments {
		instrMap[inst.ID] = inst
	}

	// Build asset class filter set.
	acFilter := make(map[string]bool, len(req.GetAssetClasses()))
	for _, ac := range req.GetAssetClasses() {
		if s := db.AssetClassToStr(ac); s != "" {
			acFilter[s] = true
		}
	}

	resp := &apiv1.ListPriceGapsResponse{
		PriceGaps: toPriceGapProtos(priceGaps, instrMap, acFilter),
		FxGaps:    toPriceGapProtos(fxGaps, instrMap, acFilter),
	}
	return resp, nil
}

// toPriceGapProtos converts DB gap ranges to proto, filtering by asset class and
// picking the best identifier per instrument.
func toPriceGapProtos(gaps []db.InstrumentDateRanges, instrMap map[string]*db.InstrumentRow, acFilter map[string]bool) []*apiv1.PriceGap {
	var out []*apiv1.PriceGap
	for _, g := range gaps {
		inst := instrMap[g.InstrumentID]
		if inst == nil {
			continue
		}
		ac := ""
		if inst.AssetClass != nil {
			ac = *inst.AssetClass
		}
		if len(acFilter) > 0 && !acFilter[ac] {
			continue
		}
		ident := bestIdentifier(inst.Identifiers)
		if ident == nil {
			continue
		}
		dateRanges := make([]*apiv1.DateRange, 0, len(g.Ranges))
		for _, r := range g.Ranges {
			dateRanges = append(dateRanges, &apiv1.DateRange{
				From: r.From.Format("2006-01-02"),
				To:   r.To.Format("2006-01-02"),
			})
		}
		pg := &apiv1.PriceGap{
			InstrumentId: g.InstrumentID,
			Identifier:   ident,
			AssetClass:   db.StrToAssetClass(ac),
			Exchange:     inst.Exchange,
			Name:         derefStr(inst.Name),
			Currency:     derefStr(inst.Currency),
			Gaps:         dateRanges,
		}
		out = append(out, pg)
	}
	return out
}

// identifierPriority defines the preference order for picking the best identifier.
var identifierPriority = map[string]int{
	"MIC_TICKER":      0,
	"OPENFIGI_TICKER": 1,
	"FX_PAIR":         2,
	"ISIN":            3,
}

// bestIdentifier picks the most useful identifier for external price lookup.
func bestIdentifier(ids []db.IdentifierInput) *apiv1.InstrumentIdentifier {
	var best *apiv1.InstrumentIdentifier
	bestPri := len(identifierPriority) + 1
	for _, id := range ids {
		pri, ok := identifierPriority[id.Type]
		if !ok {
			continue
		}
		if pri < bestPri {
			bestPri = pri
			best = &apiv1.InstrumentIdentifier{
				Type:      identifierTypeFromString(id.Type),
				Value:     id.Value,
				Domain:    id.Domain,
				Canonical: id.Canonical,
			}
		}
	}
	return best
}
