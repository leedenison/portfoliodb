package corporateevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/shopspring/decimal"
)

// ProcessPendingOptionSplits adjusts every option whose stored identity predates
// an effective split on its underlying. underlyingID == "" sweeps every option;
// a non-empty value restricts the sweep to one underlying.
//
// The work list comes from the database, not from the caller: which splits an
// option still needs is a function of its OCC symbol's own valid_from against
// each split's ex_date, not of which splits happened to arrive in this fetch
// cycle. That makes the pass idempotent, safe to run on every cycle, and
// self-retrying -- an option whose restatement failed is simply still pending
// next time.
//
// Each option is adjusted once, by the cumulative factor of all its pending
// splits, in a single transaction. Applying them one at a time against a row
// read before the first write would compound the wrong strike and leave the
// option carrying an OCC identifier per split.
//
// There is no clock parameter: the future-dated split cutoff is applied by the
// query against CURRENT_DATE, so a split fetched by the lookahead is simply not
// pending until it takes effect.
func ProcessPendingOptionSplits(ctx context.Context, database db.DB, underlyingID string, log *slog.Logger) []*db.InstrumentRow {
	pending, err := database.ListPendingOptionSplits(ctx, underlyingID)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: list pending", "underlying", underlyingID, "err", err)
		}
		return nil
	}

	// Non-whole splits are reported once per (underlying, ex_date) listing every
	// option they block, matching the shape of the event this pass has always
	// raised.
	type blockedSplit struct {
		split   db.StockSplit
		options []*db.InstrumentRow
	}
	blocked := make(map[string]*blockedSplit)
	var blockedOrder []string

	var adjusted []*db.InstrumentRow
	for _, p := range pending {
		// One cumulative factor per split, each an exact rational over the
		// splits up to and including it. The quotient is never taken here:
		// applyOptionSplits multiplies the option's stored strike by the
		// denominator before dividing by the numerator, so every name is one
		// rounding away from the strike on record rather than from the name
		// before it.
		num, den := decimal.NewFromInt(1), decimal.NewFromInt(1)
		factors := make([]factor, 0, len(p.Splits))
		unhandled := false
		for _, s := range p.Splits {
			if !IsWholeForwardSplit(s.SplitFrom, s.SplitTo) {
				key := s.InstrumentID + "\x00" + s.ExDate.Format(time.RFC3339)
				b, ok := blocked[key]
				if !ok {
					b = &blockedSplit{split: s}
					blocked[key] = b
					blockedOrder = append(blockedOrder, key)
				}
				b.options = append(b.options, p.Option)
				unhandled = true
				continue
			}
			from, _ := decimal.NewFromString(s.SplitFrom)
			to, _ := decimal.NewFromString(s.SplitTo)
			num = num.Mul(to)
			den = den.Mul(from)
			factors = append(factors, factor{split: s, num: num, den: den})
		}
		// A pending split we cannot apply blocks the whole option: restating
		// only the splits either side of it would silently produce a strike
		// that matches no real contract. Leaving the OCC symbol in force where
		// it is keeps the option pending, so it is picked up again once the
		// event is resolved.
		if unhandled {
			continue
		}
		if applyOptionSplits(ctx, database, p.Option, factors, log) {
			adjusted = append(adjusted, p.Option)
		}
	}

	for _, key := range blockedOrder {
		b := blocked[key]
		insertUnhandledUnderlyingSplit(ctx, database, b.split.InstrumentID, b.options, b.split, "non-standard split ratio", log)
	}
	return adjusted
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

// factor is one pending split and the cumulative rational over every pending
// split up to and including it.
type factor struct {
	split    db.StockSplit
	num, den decimal.Decimal
}

// applyOptionSplits mints one OCC symbol per pending split, each valid from that
// split's ex_date, and moves the option's strike to the last of them. Returns
// true when the restatement was applied.
//
// Every name is minted, not just the last. Splitting twice in one contract's
// life is vanishingly rare, but the names in between were real: a file exported
// in that window states one, and without the row it has nothing to resolve to.
func applyOptionSplits(ctx context.Context, database db.DB, opt *db.InstrumentRow, factors []factor, log *slog.Logger) bool {
	split := factors[len(factors)-1].split // most recent, for unhandled-event context

	if opt.Strike == nil || opt.Expiry == nil || opt.PutCall == nil {
		if log != nil {
			log.WarnContext(ctx, "option splits: missing option fields", "option", opt.ID)
		}
		return false
	}

	// The OCC symbol in force. A closed one names the contract as it stood
	// before an earlier split and is not what this restatement builds on.
	var currentOCC string
	for _, idn := range opt.Identifiers {
		if idn.Ref.Type == "OCC" && idn.ValidBefore == nil {
			currentOCC = idn.Ref.Value
			break
		}
	}
	if currentOCC == "" {
		if log != nil {
			log.WarnContext(ctx, "option splits: no OCC identifier", "option", opt.ID)
		}
		return false
	}

	parsed, ok := derivative.ParseOptionTicker(currentOCC)
	if !ok {
		insertUnhandledOptionSplit(ctx, database, opt, split, fmt.Sprintf("unparseable OCC identifier %q", currentOCC), log)
		return false
	}

	mints := make([]db.OptionMint, 0, len(factors))
	for _, f := range factors {
		// From the stored strike and the cumulative factor, so the rounding
		// happens once per name rather than accumulating along the chain.
		strike := derivative.AdjustStrike(*opt.Strike, f.num, f.den)
		occ, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, strike)
		if !ok {
			insertUnhandledOptionSplit(ctx, database, opt, f.split, fmt.Sprintf("cannot build OCC with adjusted strike %s", strike), log)
			return false
		}
		mints = append(mints, db.OptionMint{
			ExDate: f.split.ExDate,
			OCC: db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "OCC", Value: occ},
				Canonical: true,
			},
			Strike: strike,
		})
	}

	// All mutations run in a single transaction via ApplyOptionSplit so partial
	// failure cannot leave the option in an inconsistent state. That transaction
	// closes the OCC symbol in force, which is what removes the option from the
	// pending set. A failure here leaves it pending, so the next cycle retries
	// it.
	params := db.OptionSplitParams{
		InstrumentID: opt.ID,
		OldOCCValue:  currentOCC,
		Mints:        mints,
	}
	if err := database.ApplyOptionSplit(ctx, params); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: apply", "option", opt.ID, "err", err)
		}
		return false
	}

	if log != nil {
		last := mints[len(mints)-1]
		log.InfoContext(ctx, "option splits: restated",
			"option", opt.ID, "old_occ", currentOCC, "new_occ", last.OCC.Ref.Value,
			"old_strike", *opt.Strike, "new_strike", last.Strike,
			"splits", len(factors))
	}
	return true
}

// insertUnhandledUnderlyingSplit inserts a single unhandled event on the
// underlying instrument, listing all affected option IDs in the JSONB data.
func insertUnhandledUnderlyingSplit(ctx context.Context, database db.DB, underlyingID string, options []*db.InstrumentRow, split db.StockSplit, reason string, log *slog.Logger) {
	optionIDs := make([]string, len(options))
	for i, opt := range options {
		optionIDs[i] = opt.ID
	}
	data, _ := json.Marshal(map[string]any{
		"split_from": split.SplitFrom,
		"split_to":   split.SplitTo,
		"option_ids": optionIDs,
	})
	eventType := "NON_WHOLE_SPLIT"
	from, _ := new(big.Rat).SetString(split.SplitFrom)
	to, _ := new(big.Rat).SetString(split.SplitTo)
	if from != nil && to != nil && to.Cmp(from) < 0 {
		eventType = "REVERSE_SPLIT"
	}
	event := db.UnhandledCorporateEvent{
		InstrumentID: underlyingID,
		EventType:    eventType,
		ExDate:       &split.ExDate,
		Detail:       fmt.Sprintf("Underlying %s: %s (split %s:%s) affects %d options", underlyingID, reason, split.SplitFrom, split.SplitTo, len(options)),
		Data:         data,
	}
	if err := database.InsertUnhandledCorporateEvent(ctx, event); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: insert unhandled event", "underlying", underlyingID, "err", err)
		}
	}
}

// insertUnhandledOptionSplit inserts an unhandled event for a single option
// (used when per-option context matters, e.g. OCC build failure).
func insertUnhandledOptionSplit(ctx context.Context, database db.DB, opt *db.InstrumentRow, split db.StockSplit, reason string, log *slog.Logger) {
	data, _ := json.Marshal(map[string]string{
		"split_from":    split.SplitFrom,
		"split_to":      split.SplitTo,
		"underlying_id": split.InstrumentID,
	})
	eventType := "NON_WHOLE_SPLIT"
	from, _ := new(big.Rat).SetString(split.SplitFrom)
	to, _ := new(big.Rat).SetString(split.SplitTo)
	if from != nil && to != nil && to.Cmp(from) < 0 {
		eventType = "REVERSE_SPLIT"
	}
	event := db.UnhandledCorporateEvent{
		InstrumentID: opt.ID,
		EventType:    eventType,
		ExDate:       &split.ExDate,
		Detail:       fmt.Sprintf("Option %s: %s (split %s:%s on underlying)", opt.ID, reason, split.SplitFrom, split.SplitTo),
		Data:         data,
	}
	if err := database.InsertUnhandledCorporateEvent(ctx, event); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: insert unhandled event", "option", opt.ID, "err", err)
		}
	}
}
