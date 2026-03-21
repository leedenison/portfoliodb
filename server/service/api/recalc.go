package api

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// RecalcInitializeTx recomputes the INITIALIZE tx for a single holding declaration.
// Called when a real tx changes within the declaration's date range.
func RecalcInitializeTx(ctx context.Context, database db.DB, decl *db.HoldingDeclarationRow) error {
	startDate, err := database.GetPortfolioStartDate(ctx, decl.UserID)
	if err != nil {
		return fmt.Errorf("get portfolio start date: %w", err)
	}
	if startDate == nil {
		// No real txs remain; delete the INITIALIZE tx if it exists.
		return database.DeleteInitializeTx(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID)
	}
	startDay := startDate.Truncate(24 * time.Hour)
	if decl.AsOfDate.Before(startDay) {
		// Start date moved past declaration date; delete declaration and INITIALIZE tx.
		slog.Warn("portfolio start date moved past declaration as_of_date; deleting declaration",
			"user_id", decl.UserID, "declaration_id", decl.ID,
			"as_of_date", decl.AsOfDate.Format("2006-01-02"),
			"start_date", startDay.Format("2006-01-02"))
		if err := database.DeleteInitializeTx(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID); err != nil {
			return fmt.Errorf("delete initialize tx: %w", err)
		}
		return database.DeleteHoldingDeclaration(ctx, decl.ID)
	}
	declaredQty, err := strconv.ParseFloat(decl.DeclaredQty, 64)
	if err != nil {
		return fmt.Errorf("parse declared_qty: %w", err)
	}
	endOfAsOf := decl.AsOfDate.Add(24*time.Hour - time.Nanosecond)
	runningBalance, err := database.ComputeRunningBalance(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID, startDay, endOfAsOf)
	if err != nil {
		return fmt.Errorf("compute running balance: %w", err)
	}
	initQty := declaredQty - runningBalance
	return database.UpsertInitializeTx(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID, startDay, initQty)
}

// RecalcAllInitializeTxs recomputes all INITIALIZE txs for a user.
// Called when the portfolio start date may have changed (e.g. after bulk tx replace).
func RecalcAllInitializeTxs(ctx context.Context, database db.DB, userID string) error {
	decls, err := database.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		return fmt.Errorf("list declarations: %w", err)
	}
	if len(decls) == 0 {
		return nil
	}
	for _, decl := range decls {
		if err := RecalcInitializeTx(ctx, database, decl); err != nil {
			return fmt.Errorf("recalc declaration %s: %w", decl.ID, err)
		}
	}
	return nil
}
