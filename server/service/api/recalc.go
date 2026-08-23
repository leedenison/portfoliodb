package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

// RecalcAllInitializeTxs recomputes every INITIALIZE tx for a user. Called when the
// portfolio start date may have changed (e.g. after bulk tx replace).
//
// The unit of recalculation is the holding, not the declaration: which declaration
// pads a holding depends on the others, so a single declaration is not enough to
// know whether it is the pad. Declarations that the start date has moved past are
// pruned first, and each surviving holding's earliest declaration becomes its pad.
func RecalcAllInitializeTxs(ctx context.Context, database db.DB, userID string) error {
	decls, err := database.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		return fmt.Errorf("list declarations: %w", err)
	}
	if len(decls) == 0 {
		return nil
	}

	startDate, err := database.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		return fmt.Errorf("get portfolio start date: %w", err)
	}
	if startDate == nil {
		// No real txs remain, so there is no start date to anchor a pad to. The
		// declarations are what the user said and survive; the derived pads do not.
		for key := range byHolding(decls) {
			if err := database.DeleteInitializeTx(ctx, key); err != nil {
				return fmt.Errorf("delete pad for %s: %w", holdingLabel(key), err)
			}
		}
		return nil
	}
	startDay := startDate.Truncate(24 * time.Hour)

	before := byHolding(decls)
	kept, err := pruneBeforeStart(ctx, database, decls, startDay)
	if err != nil {
		return err
	}
	after := byHolding(kept)

	// A holding whose last declaration was pruned has nothing left to pad to.
	for key := range before {
		if len(after[key]) == 0 {
			if err := database.DeleteInitializeTx(ctx, key); err != nil {
				return fmt.Errorf("delete pad for %s: %w", holdingLabel(key), err)
			}
		}
	}

	// Batch-load instruments for asset class lookup.
	instIDs := make([]string, 0, len(kept))
	for _, decl := range kept {
		instIDs = append(instIDs, decl.InstrumentID)
	}
	instByID := map[string]*db.InstrumentRow{}
	if len(instIDs) > 0 {
		instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
		if err != nil {
			return fmt.Errorf("load instruments: %w", err)
		}
		for _, r := range instRows {
			instByID[r.ID] = r
		}
	}

	for key, group := range byHolding(kept) {
		pad := padDeclaration(group)
		if err := recalcHoldingPad(ctx, database, pad, startDay, instByID); err != nil {
			return fmt.Errorf("recalc pad for %s: %w", holdingLabel(key), err)
		}
	}
	return nil
}

// pruneBeforeStart deletes the declarations the portfolio start date has moved past
// -- a statement about a date the history no longer reaches cannot be padded to or
// checked against -- and returns those that survive. Only the declaration rows go
// here; the caller settles each holding's pad afterwards from what is left, so this
// does not need the atomic delete the user-facing path uses.
func pruneBeforeStart(ctx context.Context, database db.DB, decls []*db.HoldingDeclarationRow, startDay time.Time) ([]*db.HoldingDeclarationRow, error) {
	kept := make([]*db.HoldingDeclarationRow, 0, len(decls))
	for _, decl := range decls {
		if !decl.AsOfDate.Before(startDay) {
			kept = append(kept, decl)
			continue
		}
		slog.Warn("portfolio start date moved past declaration as_of_date; deleting declaration",
			"user_id", decl.UserID, "declaration_id", decl.ID,
			"as_of_date", decl.AsOfDate.Format("2006-01-02"),
			"start_date", startDay.Format("2006-01-02"))
		if err := database.DeleteHoldingDeclaration(ctx, decl.ID); err != nil {
			return nil, fmt.Errorf("delete declaration %s: %w", decl.ID, err)
		}
	}
	return kept, nil
}

// recalcHoldingPad rewrites a holding's INITIALIZE tx from the declaration that pads
// it. instByID may be nil, in which case the instrument is loaded on demand.
func recalcHoldingPad(ctx context.Context, database db.DB, decl *db.HoldingDeclarationRow, startDay time.Time, instByID map[string]*db.InstrumentRow) error {
	declaredQty, err := decimal.NewFromString(decl.DeclaredQty)
	if err != nil {
		return fmt.Errorf("parse declared_qty: %w", err)
	}
	dayAfterAsOf := decl.AsOfDate.AddDate(0, 0, 1)
	runningBalance, err := database.ComputeRunningBalance(ctx, keyOf(decl), startDay, dayAfterAsOf, decl.ShareCountBasis)
	if err != nil {
		return fmt.Errorf("compute running balance: %w", err)
	}
	// A zero pad means the real transactions already account for the declared
	// balance exactly. That is the declaration agreeing with the data, not being
	// superseded by it, and it is the outcome a checked declaration exists to
	// report -- so the record stays and the pad is written as zero.
	initQty := declaredQty.Sub(runningBalance)
	return database.UpsertInitializeTx(ctx, keyOf(decl), db.InitializeTx{
		Timestamp:       startDay,
		Quantity:        initQty,
		ShareCountBasis: decl.ShareCountBasis,
	})
}

// keyOf is the holding a declaration is about. db.Holding is comparable, so it is
// the map key here as well as the argument the write paths take, and the two
// cannot drift into disagreeing about what identifies a holding.
func keyOf(decl *db.HoldingDeclarationRow) db.Holding {
	return db.Holding{
		UserID:       decl.UserID,
		Broker:       decl.Broker,
		Account:      decl.Account,
		InstrumentID: decl.InstrumentID,
		ListingID:    decl.ListingID,
	}
}

// holdingLabel names a holding in an error. The line is included only when there
// is one, so the ordinary single-line message reads as it did.
func holdingLabel(h db.Holding) string {
	if h.ListingID == "" {
		return fmt.Sprintf("%s/%s/%s", h.Broker, h.Account, h.InstrumentID)
	}
	return fmt.Sprintf("%s/%s/%s/%s", h.Broker, h.Account, h.InstrumentID, h.ListingID)
}

func byHolding(decls []*db.HoldingDeclarationRow) map[db.Holding][]*db.HoldingDeclarationRow {
	out := map[db.Holding][]*db.HoldingDeclarationRow{}
	for _, decl := range decls {
		out[keyOf(decl)] = append(out[keyOf(decl)], decl)
	}
	return out
}
