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
	"github.com/leedenison/portfoliodb/server/service/identification"
)

// ProcessPendingOptionSplits adjusts every option whose stored identity predates
// an effective split on its underlying. underlyingID == "" sweeps every option;
// a non-empty value restricts the sweep to one underlying.
//
// The work list comes from the database, not from the caller: which splits an
// option still needs is a function of its identity_as_of against each split's
// ex_date, not of which splits happened to arrive in this fetch cycle. That
// makes the pass idempotent, safe to run on every cycle, and self-retrying --
// an option whose adjustment failed is simply still pending next time.
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
		// Compound as exact rationals and convert once. Every ratio the
		// IsWholeForwardSplit guard admits is a whole number, so float
		// multiplication would happen to be exact today, but that stops holding
		// the moment the guard admits a fractional ratio.
		factorRat := new(big.Rat).SetInt64(1)
		unhandled := false
		for _, s := range p.Splits {
			if !identification.IsWholeForwardSplit(s.SplitFrom, s.SplitTo) {
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
			from, _ := new(big.Rat).SetString(s.SplitFrom)
			to, _ := new(big.Rat).SetString(s.SplitTo)
			factorRat.Mul(factorRat, new(big.Rat).Quo(to, from))
		}
		factor, _ := factorRat.Float64()
		// A pending split we cannot apply blocks the whole option: adjusting
		// only the splits either side of it would silently produce a strike
		// that matches no real contract. Leaving identity_as_of untouched keeps
		// the option pending, so it is picked up again once the event is
		// resolved.
		if unhandled {
			continue
		}
		if applyOptionSplits(ctx, database, p.Option, p.Splits, factor, log) {
			adjusted = append(adjusted, p.Option)
		}
	}

	for _, key := range blockedOrder {
		b := blocked[key]
		insertUnhandledUnderlyingSplit(ctx, database, b.split.InstrumentID, b.options, b.split, "non-standard split ratio", log)
	}
	return adjusted
}

// applyOptionSplits rewrites one option's OCC symbol and strike for the
// cumulative factor of its pending splits. Returns true when the adjustment was
// applied. splits is used only for reporting; factor already accounts for all of
// them.
func applyOptionSplits(ctx context.Context, database db.DB, opt *db.InstrumentRow, splits []db.StockSplit, factor float64, log *slog.Logger) bool {
	split := splits[len(splits)-1] // most recent, for unhandled-event context

	if opt.Strike == nil || opt.Expiry == nil || opt.PutCall == nil {
		if log != nil {
			log.WarnContext(ctx, "option splits: missing option fields", "option", opt.ID)
		}
		return false
	}

	// Find the current OCC identifier.
	var currentOCC string
	for _, idn := range opt.Identifiers {
		if idn.Type == "OCC" {
			currentOCC = idn.Value
			break
		}
	}
	if currentOCC == "" {
		if log != nil {
			log.WarnContext(ctx, "option splits: no OCC identifier", "option", opt.ID)
		}
		return false
	}

	newStrike := *opt.Strike / factor

	// Build new OCC.
	parsed, ok := derivative.ParseOptionTicker(currentOCC)
	if !ok {
		insertUnhandledOptionSplit(ctx, database, opt, split, fmt.Sprintf("unparseable OCC identifier %q", currentOCC), log)
		return false
	}

	newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike)
	if !ok {
		insertUnhandledOptionSplit(ctx, database, opt, split, fmt.Sprintf("cannot build OCC with adjusted strike %.4f", newStrike), log)
		return false
	}

	// All mutations run in a single transaction via ApplyOptionSplit so
	// partial failure cannot leave the option in an inconsistent state. That
	// transaction also advances identity_as_of, which is what removes the option
	// from the pending set. A failure here leaves it pending, so the next cycle
	// retries it.
	params := db.OptionSplitParams{
		InstrumentID: opt.ID,
		OldOCCValue:  currentOCC,
		NewOCC:       db.IdentifierInput{Type: "OCC", Value: newOCC, Canonical: true},
		NewStrike:    newStrike,
		NewName:      newOCC,
	}
	if err := database.ApplyOptionSplit(ctx, params); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: apply", "option", opt.ID, "err", err)
		}
		return false
	}

	if log != nil {
		log.InfoContext(ctx, "option splits: adjusted",
			"option", opt.ID, "old_occ", currentOCC, "new_occ", newOCC,
			"old_strike", *opt.Strike, "new_strike", newStrike,
			"splits", len(splits), "factor", factor)
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
