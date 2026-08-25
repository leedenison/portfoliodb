package api

import (
	"context"
	"time"

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

	// A gap names a line; the security above it carries the name, the identifier
	// and the asset class a person reads the gap by.
	idSet := make(map[string]bool, len(priceGaps)+len(fxGaps))
	for _, g := range priceGaps {
		idSet[g.ListingID] = true
	}
	for _, g := range fxGaps {
		idSet[g.ListingID] = true
	}
	listingIDs := make([]string, 0, len(idSet))
	for id := range idSet {
		listingIDs = append(listingIDs, id)
	}

	listingMap, err := s.db.ListingsByIDs(ctx, listingIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list listings: %v", err)
	}
	instIDSet := make(map[string]bool, len(listingMap))
	for _, l := range listingMap {
		instIDSet[l.InstrumentID] = true
	}
	instIDs := make([]string, 0, len(instIDSet))
	for id := range instIDSet {
		instIDs = append(instIDs, id)
	}

	instruments, err := s.db.ListInstrumentsByIDs(ctx, instIDs)
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
		PriceGaps: toPriceGapProtos(priceGaps, listingMap, instrMap, acFilter),
		FxGaps:    toPriceGapProtos(fxGaps, listingMap, instrMap, acFilter),
	}
	return resp, nil
}

// toPriceGapProtos converts DB gap ranges to proto, filtering by asset class and
// picking the best identifier of the security the line belongs to. Gap end dates
// are clamped to today (exclusive) so that the current day -- which typically has
// no close price yet -- is excluded.
func toPriceGapProtos(gaps []db.ListingDateRanges, listingMap map[string]*db.Listing, instrMap map[string]*db.InstrumentRow, acFilter map[string]bool) []*apiv1.PriceGap {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var out []*apiv1.PriceGap
	for _, g := range gaps {
		lst := listingMap[g.ListingID]
		if lst == nil {
			continue
		}
		inst := instrMap[lst.InstrumentID]
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
		// A gap is reported to a person, who knows the security by its ticker.
		// Both grains, until 0154 says which one a gap is at.
		ident := bestIdentifier(inst.AllIdentifiers())
		if ident == nil {
			continue
		}
		dateRanges := make([]*apiv1.DateRange, 0, len(g.Ranges))
		for _, r := range g.Ranges {
			before := r.Before
			if before.After(today) {
				before = today
			}
			if !r.From.Before(before) {
				continue // empty range after clamping
			}
			dateRanges = append(dateRanges, &apiv1.DateRange{
				From:   r.From.Format("2006-01-02"),
				Before: before.Format("2006-01-02"),
			})
		}
		if len(dateRanges) == 0 {
			continue
		}
		pg := &apiv1.PriceGap{
			ListingId:    g.ListingID,
			Currency:     lst.Currency,
			InstrumentId: lst.InstrumentID,
			Identifier:   ident,
			AssetClass:   db.StrToAssetClass(ac),
			Venues:       venueMICs(lst.Venues),
			Name:         derefStr(inst.Name),
			Gaps:         dateRanges,
		}
		out = append(out, pg)
	}
	return out
}

// venueMICs is the MICs of a line's venues, for a consumer that wants the venue
// and not the reference data behind it.
func venueMICs(venues []db.Venue) []string {
	if len(venues) == 0 {
		return nil
	}
	out := make([]string, 0, len(venues))
	for _, v := range venues {
		out = append(out, v.MIC)
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
		pri, ok := identifierPriority[id.Ref.Type]
		if !ok {
			continue
		}
		if pri < bestPri {
			bestPri = pri
			best = &apiv1.InstrumentIdentifier{
				Type:      identifierTypeFromString(id.Ref.Type),
				Value:     id.Ref.Value,
				Domain:    id.Ref.Domain,
				Canonical: id.Canonical,
			}
		}
	}
	return best
}
