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
	return recalcInitializeTx(ctx, database, decl, nil)
}

func recalcInitializeTx(ctx context.Context, database db.DB, decl *db.HoldingDeclarationRow, instByID map[string]*db.InstrumentRow) error {
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
		return database.DeleteDeclarationWithInitializeTx(ctx, decl.ID, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID)
	}
	declaredQty, err := strconv.ParseFloat(decl.DeclaredQty, 64)
	if err != nil {
		return fmt.Errorf("parse declared_qty: %w", err)
	}
	dayAfterAsOf := decl.AsOfDate.AddDate(0, 0, 1)
	runningBalance, err := database.ComputeRunningBalance(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID, startDay, dayAfterAsOf)
	if err != nil {
		return fmt.Errorf("compute running balance: %w", err)
	}
	initQty := declaredQty - runningBalance
	if initQty == 0 {
		// Real txs already fully account for the declared balance at as_of_date;
		// the declaration is superseded by real data. Drop both atomically.
		slog.Info("real txs fully account for declared balance; deleting declaration",
			"user_id", decl.UserID, "declaration_id", decl.ID)
		return database.DeleteDeclarationWithInitializeTx(ctx, decl.ID, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID)
	}
	var assetClass string
	if inst := instByID[decl.InstrumentID]; inst != nil && inst.AssetClass != nil {
		assetClass = *inst.AssetClass
	} else if instByID == nil {
		inst, err := database.GetInstrument(ctx, decl.InstrumentID)
		if err == nil && inst != nil && inst.AssetClass != nil {
			assetClass = *inst.AssetClass
		}
	}
	txType := txTypeForAssetClass(assetClass, initQty)
	return database.UpsertInitializeTx(ctx, decl.UserID, decl.Broker, decl.Account, decl.InstrumentID, txType, startDay, initQty)
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
	// Batch-load instruments for asset class lookup
	instIDs := make([]string, 0, len(decls))
	for _, decl := range decls {
		instIDs = append(instIDs, decl.InstrumentID)
	}
	instRows, err := database.ListInstrumentsByIDs(ctx, instIDs)
	if err != nil {
		return fmt.Errorf("load instruments: %w", err)
	}
	instByID := make(map[string]*db.InstrumentRow, len(instRows))
	for _, r := range instRows {
		instByID[r.ID] = r
	}
	for _, decl := range decls {
		if err := recalcInitializeTx(ctx, database, decl, instByID); err != nil {
			return fmt.Errorf("recalc declaration %s: %w", decl.ID, err)
		}
	}
	return nil
}
