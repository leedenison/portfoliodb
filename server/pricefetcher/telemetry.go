package pricefetcher

import (
	"context"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
)

// priceGaps owns the telemetry rows for one cycle's gap list.
//
// Rows are written for the whole list before any fetching starts, so a cycle that
// dies part way leaves the instruments it never reached unstamped and where it
// stopped stays readable. Writing each row as the loop arrived at it would leave
// no trace of those at all, which is the question asked first when a cycle is
// killed: how far did it get.
//
// A nil *priceGaps records nothing and every method is safe to call on one, which
// is what a caller with no telemetry writer gets.
type priceGaps struct {
	tel   db.TelemetryDB
	runID string
	// ids is parallel to the gap list rather than keyed by instrument id, so two
	// gaps naming one instrument cannot collide. An empty entry is a write that
	// failed, which the writer already skips the children of.
	ids []string
	// calls counts the rows written under each gap, which is what separates a gap
	// every eligible plugin had already covered from one no plugin would take.
	calls []int
}

// newPriceGaps writes a row per gap and returns the ledger that stamps them.
//
// fxFrom is where FXGaps' entries start in the concatenated list. The two are
// fetched by one loop and are indistinguishable from there on, and they are not
// the same size of problem: a missing rate breaks valuation for every instrument
// denominated in that currency rather than for one.
//
// listingByID and instByID may be nil, for the caller that has established there
// is no plugin to put any of these to and has not loaded them. The three
// attributes they supply explain plugin filtering, and no filtering happens on
// that path.
func newPriceGaps(ctx context.Context, tel db.TelemetryDB, runID string,
	gaps []db.ListingDateRanges, fxFrom int, listingByID map[string]*db.Listing,
	instByID map[string]*db.InstrumentRow) *priceGaps {
	if tel == nil || runID == "" {
		return nil
	}
	g := &priceGaps{
		tel:   tel,
		runID: runID,
		ids:   make([]string, len(gaps)),
		calls: make([]int, len(gaps)),
	}
	for i, ig := range gaps {
		row := db.TelemetryPriceGap{
			RunID:           runID,
			ListingID:       ig.ListingID,
			IsFX:            i >= fxFrom,
			DaysOutstanding: rangesDays(ig.Ranges),
		}
		// The gap is a line, and the panel asking whether an instrument ever
		// prices reads by security, so both are recorded.
		//
		// The currency and the venues are the line's own, because they are what
		// PluginAcceptsListing compares and this row exists to explain that
		// comparison. The venue set is joined rather than reduced to one: a
		// plugin carrying any of them accepts the line, so any one of them alone
		// would misreport the decision.
		lst := listingByID[ig.ListingID]
		if lst != nil {
			row.InstrumentID = lst.InstrumentID
			row.Currency = lst.Currency
			row.Exchange = strings.Join(lst.Venues, ",")
		}
		if inst := instByID[row.InstrumentID]; inst != nil && inst.AssetClass != nil {
			row.AssetClass = *inst.AssetClass
		}
		g.ids[i] = tel.StartPriceGap(ctx, row)
	}
	return g
}

// end stamps what became of the gap at i.
func (g *priceGaps) end(ctx context.Context, i int, outcome string) {
	if g == nil {
		return
	}
	g.tel.EndPriceGap(ctx, g.runID, g.ids[i], outcome)
}

// endAll stamps every gap with one outcome, for the cycle that finds no plugin to
// put any of them to.
func (g *priceGaps) endAll(ctx context.Context, outcome string) {
	if g == nil {
		return
	}
	for i := range g.ids {
		g.end(ctx, i, outcome)
	}
}

// call records one range put to one plugin under the gap at i, filling in the ids
// the caller does not hold.
func (g *priceGaps) call(ctx context.Context, i int, c db.TelemetryPricePluginCall) {
	if g == nil {
		return
	}
	c.RunID, c.GapID = g.runID, g.ids[i]
	g.calls[i]++
	g.tel.WritePricePluginCall(ctx, c)
}

// called reports whether any plugin call was written under the gap at i.
func (g *priceGaps) called(i int) bool {
	if g == nil {
		return false
	}
	return g.calls[i] > 0
}

// gapOutcome names what became of one instrument's gap.
//
// fetched is a plugin having covered every outstanding range without error, bars
// the rows that arrived, reached whether any plugin passed the filters, and called
// whether any of them was actually asked.
//
// The two settled_empty paths are the ones worth reading twice. A plugin that
// covered every range and returned nothing has settled the gap -- the instrument
// did not trade then, or no plugin reaches that far back -- and so has a gap every
// eligible plugin had already covered on an earlier cycle. Neither is a failure,
// and counting them as one would fill a panel meant to show outages with untraded
// weeks.
func gapOutcome(fetched bool, bars int, reached, called bool) string {
	switch {
	case fetched && bars > 0:
		return db.TelemetryGapFilled
	case fetched:
		return db.TelemetryGapSettledEmpty
	case !reached:
		return db.TelemetryGapNoEligiblePlugin
	case !called:
		return db.TelemetryGapSettledEmpty
	}
	return db.TelemetryGapAllPluginsFailed
}

// rangesDays is how much history a gap was asking for, in whole days. The
// orchestrator's ranges are half-open and UTC-truncated, so the subtraction is
// exact and no calendar arithmetic is needed.
func rangesDays(ranges []db.DateRange) int {
	days := 0
	for _, r := range ranges {
		if r.Before.After(r.From) {
			days += int(r.Before.Sub(r.From) / db.Day)
		}
	}
	return days
}
