package identification

import (
	"context"
	"math"
	"math/big"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// AdjustOCCForKnownSplits checks whether the underlying ticker parsed from an
// OCC identifier has any known stock splits with ex_date <= today. If so, the
// OCC strike is adjusted by the cumulative split factor and a new compact OCC
// is returned. Returns the original hints unmodified when no adjustment is
// needed (no splits found, underlying not in DB, or not an OCC hint).
func AdjustOCCForKnownSplits(ctx context.Context, database db.CorporateEventDB, hints []identifier.Identifier) []identifier.Identifier {
	adjusted := make([]identifier.Identifier, len(hints))
	copy(adjusted, hints)

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

		factor := cumulativeSplitFactor(splits, time.Now())
		if factor == 1.0 {
			continue
		}

		newStrike := parsed.Strike / factor
		newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike)
		if !ok {
			continue
		}

		adjusted[i] = identifier.Identifier{Type: h.Type, Domain: h.Domain, Value: newOCC}
	}
	return adjusted
}

// cumulativeSplitFactor computes the product of (split_to / split_from) for
// all splits with ex_date <= asOf. Returns 1.0 when no applicable splits.
func cumulativeSplitFactor(splits []db.StockSplit, asOf time.Time) float64 {
	factor := 1.0
	today := asOf.Truncate(24 * time.Hour)
	for _, s := range splits {
		if s.ExDate.After(today) {
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
