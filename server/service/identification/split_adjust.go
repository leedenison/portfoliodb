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
// hint. timer may be nil (uses time.Now).
func AdjustOCCForKnownSplits(ctx context.Context, database db.CorporateEventDB, hints []identifier.Identifier, hintsValidAt *time.Time, timer *clock.Timer) []identifier.Identifier {
	if hintsValidAt == nil {
		return hints
	}
	now := timer.Now().Truncate(24 * time.Hour)
	var adjusted []identifier.Identifier

	for _, h := range hints {
		if h.Type != "OCC" {
			adjusted = append(adjusted, h)
			continue
		}
		compact, ok := derivative.OCCCompact(h.Value)
		if !ok {
			adjusted = append(adjusted, h)
			continue
		}
		parsed, ok := derivative.ParseOptionTicker(compact)
		if !ok || parsed.Symbol == "" || parsed.Strike <= 0 {
			adjusted = append(adjusted, h)
			continue
		}

		splits, err := database.SplitsByUnderlyingTicker(ctx, parsed.Symbol)
		if err != nil || len(splits) == 0 {
			adjusted = append(adjusted, h)
			continue
		}

		// Compute OCC_AT_EXPIRY for expired options: apply splits only
		// up to the expiry date so OpenFIGI receives the OCC as it was
		// when the option expired.
		expiry := parsed.Expiry.Truncate(24 * time.Hour)
		if !expiry.After(now) {
			factorAtExpiry := splitFactorBetween(splits, *hintsValidAt, expiry)
			expiryStrike := parsed.Strike / factorAtExpiry
			if expiryOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, expiryStrike); ok {
				adjusted = append(adjusted, identifier.Identifier{Type: identifier.InternalHintTypeOCCAtExpiry, Domain: h.Domain, Value: expiryOCC})
			}
		}

		// Adjust the OCC hint for DB lookups (splits up to now).
		factor := splitFactorSince(splits, *hintsValidAt, timer)
		if factor != 1.0 {
			newStrike := parsed.Strike / factor
			if newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike); ok {
				adjusted = append(adjusted, identifier.Identifier{Type: h.Type, Domain: h.Domain, Value: newOCC})
				continue
			}
		}
		adjusted = append(adjusted, h)
	}
	return adjusted
}

// splitFactorSince computes the cumulative split factor for splits that
// occurred after since and on or before today: ex_date > since AND
// ex_date <= now. Returns 1.0 when no applicable splits. timer may be
// nil (uses time.Now).
func splitFactorSince(splits []db.StockSplit, since time.Time, timer *clock.Timer) float64 {
	return splitFactorBetween(splits, since, timer.Now().Truncate(24*time.Hour))
}

// splitFactorBetween computes the cumulative split factor for splits where
// ex_date > since AND ex_date <= until. Returns 1.0 when no applicable splits.
func splitFactorBetween(splits []db.StockSplit, since, until time.Time) float64 {
	factor := 1.0
	sinceDate := since.Truncate(24 * time.Hour)
	untilDate := until.Truncate(24 * time.Hour)
	for _, s := range splits {
		if s.ExDate.After(untilDate) || !s.ExDate.After(sinceDate) {
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
