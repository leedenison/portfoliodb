package identification

import (
	"context"
	"math/big"
	"time"

	"github.com/leedenison/portfoliodb/server/clock"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/shopspring/decimal"
)

// AdjustOCCForKnownSplits checks whether the underlying ticker parsed from an
// OCC identifier has any known stock splits that occurred after hintsValidAt.
// If so, the OCC strike is adjusted by the cumulative split factor and a new
// compact OCC is returned. Returns the original hints unmodified when
// hintsValidAt is nil, no splits found, underlying not in DB, or not an OCC
// hint. timer may be nil (uses time.Now).
//
// Rebasing stops at the option's expiry. A contract is restated only while it is
// listed, so a split with an ex_date after expiry produces a symbol and strike
// that never traded, and the stored row it would be looked up against does not
// carry them either. See adr/0036-expired-options-are-not-restated.md.
//
// The second return value is the market time the returned OCC hints reflect,
// and it is nil for "now". An OCC lookup is identity-by-value: the provider
// answers about the contract it was named, so an identity derived from these
// hints is only as current as the hints themselves. Rebasing carries a hint as
// far forward as it can go -- today, or expiry for a contract that is no longer
// listed -- but only across splits already stored. A split we have not yet
// learned of leaves the hint at its original vintage, which is what this reports
// so the caller can date the names it writes honestly. See
// adr/0055-identifier-validity-is-an-interval.md.
func AdjustOCCForKnownSplits(ctx context.Context, database db.CorporateEventDB, hints []identifier.Identifier, hintsValidAt *time.Time, timer *clock.Timer) ([]identifier.Identifier, *time.Time) {
	if hintsValidAt == nil {
		return hints, nil
	}
	now := timer.Now().Truncate(24 * time.Hour)
	var adjusted []identifier.Identifier
	// Set by any OCC hint left at its original vintage. Hints that were rebased
	// reflect now and do not constrain the stamp; the pessimistic case wins
	// because a single stale OCC is enough to make the identity stale.
	//
	// An expired contract counts as reflecting now even though it was only
	// rebased to its expiry: it will never be restated again, so that is as
	// current as its identity can get. Both stamps read the same to the only
	// consumer anyway -- the pending-split query already requires
	// ex_date <= expiry, so a stamp of either expiry or now sits on or after
	// every ex_date that could select the option.
	var vintage *time.Time

	for _, h := range hints {
		if h.Type != "OCC" {
			adjusted = append(adjusted, h)
			continue
		}
		compact, ok := derivative.OCCCompact(h.Value)
		if !ok {
			adjusted = append(adjusted, h)
			vintage = hintsValidAt
			continue
		}
		parsed, ok := derivative.ParseOptionTicker(compact)
		if !ok || parsed.Symbol == "" || !parsed.Strike.IsPositive() {
			adjusted = append(adjusted, h)
			vintage = hintsValidAt
			continue
		}

		splits, err := database.SplitsByUnderlyingTicker(ctx, parsed.Symbol)
		if err != nil || len(splits) == 0 {
			adjusted = append(adjusted, h)
			vintage = hintsValidAt
			continue
		}

		// A contract is only restated while it is listed: OCC adjusts it on the
		// effective date, so a split after expiry never touched it and the
		// furthest forward its symbol can be carried is its own expiry. For a
		// live option that bound is today and rebasing is unaffected.
		until := now
		if expiry := parsed.Expiry.Truncate(24 * time.Hour); expiry.Before(until) {
			until = expiry
		}
		num, den := splitFactorBetween(splits, *hintsValidAt, until)
		if !num.Equal(den) {
			newStrike := derivative.AdjustStrike(parsed.Strike, num, den)
			if newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike); ok {
				adjusted = append(adjusted, identifier.Identifier{Type: h.Type, Domain: h.Domain, Value: newOCC})
				continue
			}
		}
		adjusted = append(adjusted, h)
		vintage = hintsValidAt
	}
	return adjusted, vintage
}

// splitFactorBetween computes the cumulative split factor for splits where
// ex_date > since AND ex_date <= until, as an exact rational: the numerator and
// denominator are returned separately so the caller multiplies first and divides
// once. Returns 1/1 when no splits apply. This is the Go twin of the SQL
// split_factor_at; see adr/0028-cumulative-split-factor-is-an-exact-rational.md.
func splitFactorBetween(splits []db.StockSplit, since, until time.Time) (num, den decimal.Decimal) {
	num, den = decimal.NewFromInt(1), decimal.NewFromInt(1)
	sinceDate := since.Truncate(24 * time.Hour)
	untilDate := until.Truncate(24 * time.Hour)
	for _, s := range splits {
		if s.ExDate.After(untilDate) || !s.ExDate.After(sinceDate) {
			continue
		}
		from, errF := decimal.NewFromString(s.SplitFrom)
		to, errT := decimal.NewFromString(s.SplitTo)
		if errF != nil || errT != nil || !from.IsPositive() || !to.IsPositive() {
			continue
		}
		num = num.Mul(to)
		den = den.Mul(from)
	}
	return num, den
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
