package identification

import (
	"context"
	"math"
	"math/big"
	"time"

	"github.com/leedenison/portfoliodb/server/clock"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// AdjustOCCForKnownSplits checks whether the underlying ticker parsed from an
// OCC identifier has any known stock splits that occurred after hintsValidAt.
// If so, the OCC strike is adjusted by the cumulative split factor and a new
// compact OCC is returned. Returns the original hints unmodified when
// hintsValidAt is nil, no splits found, underlying not in DB, or not an OCC
// hint.
//
// The second return value contains the underlying splits that were applied to
// any OCC adjustment. Callers use this to create derived split records on
// the option instrument after identification. timer may be nil (uses
// time.Now).
func AdjustOCCForKnownSplits(ctx context.Context, database db.CorporateEventDB, hints []identifier.Identifier, hintsValidAt *time.Time, timer *clock.Timer) ([]identifier.Identifier, []db.StockSplit) {
	if hintsValidAt == nil {
		return hints, nil
	}
	adjusted := make([]identifier.Identifier, len(hints))
	copy(adjusted, hints)

	var appliedSplits []db.StockSplit

	for i, h := range adjusted {
		if h.Type != "OCC" {
			continue
		}
		compact, ok := derivative.OCCCompact(h.Value)
		if !ok {
			continue
		}
		parsed, ok := derivative.ParseOptionTicker(compact)
		if !ok || parsed.Symbol == "" || parsed.Strike <= 0 {
			continue
		}

		splits, err := database.SplitsByUnderlyingTicker(ctx, parsed.Symbol)
		if err != nil || len(splits) == 0 {
			continue
		}

		factor := splitFactorSince(splits, *hintsValidAt, timer)
		if factor == 1.0 {
			continue
		}

		newStrike := parsed.Strike / factor
		newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike)
		if !ok {
			continue
		}

		adjusted[i] = identifier.Identifier{Type: h.Type, Domain: h.Domain, Value: newOCC}
		appliedSplits = append(appliedSplits, applicableSplits(splits, *hintsValidAt, timer)...)
	}
	return adjusted, appliedSplits
}

// splitFactorSince computes the cumulative split factor for splits that
// occurred after since and on or before today: ex_date > since AND
// ex_date <= now. Returns 1.0 when no applicable splits. timer may be
// nil (uses time.Now).
func splitFactorSince(splits []db.StockSplit, since time.Time, timer *clock.Timer) float64 {
	factor := 1.0
	now := timer.Now().Truncate(24 * time.Hour)
	sinceDate := since.Truncate(24 * time.Hour)
	for _, s := range splits {
		if s.ExDate.After(now) || !s.ExDate.After(sinceDate) {
			continue
		}
		from, okF := new(big.Rat).SetString(s.SplitFrom)
		to, okT := new(big.Rat).SetString(s.SplitTo)
		if !okF || !okT || from.Sign() <= 0 {
			continue
		}
		ratio := new(big.Rat).Quo(to, from)
		f, _ := ratio.Float64()
		if f > 0 && !math.IsInf(f, 0) {
			factor *= f
		}
	}
	return factor
}

// applicableSplits returns the subset of splits with ex_date > since AND
// ex_date <= now. Used by callers that need the actual split rows (e.g.
// to create derived splits on option instruments).
func applicableSplits(splits []db.StockSplit, since time.Time, timer *clock.Timer) []db.StockSplit {
	now := timer.Now().Truncate(24 * time.Hour)
	sinceDate := since.Truncate(24 * time.Hour)
	var out []db.StockSplit
	for _, s := range splits {
		if s.ExDate.After(now) || !s.ExDate.After(sinceDate) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// IsWholeForwardSplit returns true if the split factor (split_to/split_from)
// is a whole number > 1 (e.g. 2:1, 4:1, 10:1).
func IsWholeForwardSplit(splitFrom, splitTo string) bool {
	from, okF := new(big.Rat).SetString(splitFrom)
	to, okT := new(big.Rat).SetString(splitTo)
	if !okF || !okT || from.Sign() <= 0 || to.Sign() <= 0 {
		return false
	}
	ratio := new(big.Rat).Quo(to, from)
	if !ratio.IsInt() {
		return false
	}
	return ratio.Cmp(new(big.Rat).SetInt64(1)) > 0
}
