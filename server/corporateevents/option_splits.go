package corporateevents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/service/identification"
)

// processOptionSplits adjusts options on the given underlying after new stock
// splits land. For each option and each applicable split:
//   - If identified_at >= split.fetched_at: skip (case 3 -- already correct)
//   - If factor is not a whole forward split: insert unhandled event, skip
//   - Otherwise: update OCC identifier, update strike, insert derived split row
//
// Splits are processed in chronological order.
func processOptionSplits(ctx context.Context, database db.DB, underlyingID string, splits []db.StockSplit, log *slog.Logger) {
	options, err := database.ListOptionsByUnderlying(ctx, underlyingID)
	if err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: list options", "underlying", underlyingID, "err", err)
		}
		return
	}
	if len(options) == 0 {
		return
	}

	// Sort splits chronologically.
	sorted := make([]db.StockSplit, len(splits))
	copy(sorted, splits)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ExDate.Before(sorted[j].ExDate) })

	today := time.Now().UTC().Truncate(24 * time.Hour)

	for _, split := range sorted {
		if split.ExDate.After(today) {
			continue // Don't process future-dated splits.
		}

		if !identification.IsWholeForwardSplit(split.SplitFrom, split.SplitTo) {
			// Route non-standard splits to unhandled events for each option.
			for _, opt := range options {
				insertUnhandledOptionSplit(ctx, database, opt, split, "non-standard split ratio", log)
			}
			continue
		}

		from, _ := new(big.Rat).SetString(split.SplitFrom)
		to, _ := new(big.Rat).SetString(split.SplitTo)
		ratio := new(big.Rat).Quo(to, from)
		factor, _ := ratio.Float64()

		for _, opt := range options {
			processOneOptionSplit(ctx, database, opt, split, factor, log)
		}
	}
}

func processOneOptionSplit(ctx context.Context, database db.DB, opt *db.InstrumentRow, split db.StockSplit, factor float64, log *slog.Logger) {
	// Case 3: identified after we knew about the split -- already correct.
	if opt.IdentifiedAt != nil && !opt.IdentifiedAt.Before(split.FetchedAt) {
		return
	}

	if opt.Strike == nil || opt.Expiry == nil || opt.PutCall == nil {
		if log != nil {
			log.WarnContext(ctx, "option splits: missing option fields", "option", opt.ID)
		}
		return
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
		return
	}

	newStrike := *opt.Strike / factor

	// Build new OCC.
	parsed, ok := derivative.ParseOptionTicker(currentOCC)
	if !ok {
		insertUnhandledOptionSplit(ctx, database, opt, split, fmt.Sprintf("unparseable OCC identifier %q", currentOCC), log)
		return
	}

	newOCC, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, newStrike)
	if !ok {
		insertUnhandledOptionSplit(ctx, database, opt, split, fmt.Sprintf("cannot build OCC with adjusted strike %.4f", newStrike), log)
		return
	}

	// All mutations run in a single transaction via ApplyOptionSplit so
	// partial failure cannot leave the option in an inconsistent state.
	params := db.OptionSplitParams{
		InstrumentID: opt.ID,
		OldOCCValue:  currentOCC,
		NewOCC:       db.IdentifierInput{Type: "OCC", Value: newOCC, Canonical: true},
		NewStrike:    newStrike,
		DerivedSplit: db.StockSplit{
			InstrumentID: opt.ID,
			ExDate:       split.ExDate,
			SplitFrom:    split.SplitFrom,
			SplitTo:      split.SplitTo,
			DataProvider: "derived",
		},
	}
	if err := database.ApplyOptionSplit(ctx, params); err != nil {
		if log != nil {
			log.ErrorContext(ctx, "option splits: apply", "option", opt.ID, "err", err)
		}
		return
	}

	if log != nil {
		log.InfoContext(ctx, "option splits: adjusted",
			"option", opt.ID, "old_occ", currentOCC, "new_occ", newOCC,
			"old_strike", *opt.Strike, "new_strike", newStrike,
			"split", fmt.Sprintf("%s:%s", split.SplitFrom, split.SplitTo))
	}
}

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
