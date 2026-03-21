package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl1", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "150.5", asOf)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if row.Broker != "IBKR" || row.Account != "acct1" || row.DeclaredQty != "150.5" {
		t.Fatalf("unexpected row: %+v", row)
	}
	if row.InstrumentID != instID {
		t.Fatalf("instrument_id mismatch: got %s want %s", row.InstrumentID, instID)
	}
}

func TestCreateHoldingDeclaration_DuplicateRejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl2", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "100", asOf)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "200", asOf)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestUpdateHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl3", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "MSFT", Canonical: false}}, "", nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, _ := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "100", asOf)

	newDate := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	updated, err := p.UpdateHoldingDeclaration(ctx, row.ID, "200", newDate)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DeclaredQty != "200" {
		t.Fatalf("qty not updated: got %s", updated.DeclaredQty)
	}
	if !updated.AsOfDate.Equal(newDate) {
		t.Fatalf("date not updated: got %v", updated.AsOfDate)
	}
}

func TestDeleteHoldingDeclaration(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl4", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "TSLA", Canonical: false}}, "", nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, _ := p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", instID, "50", asOf)

	if err := p.DeleteHoldingDeclaration(ctx, row.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Verify it's gone
	_, err := p.GetHoldingDeclaration(ctx, row.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteHoldingDeclaration_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	err := p.DeleteHoldingDeclaration(ctx, "00000000-0000-0000-0000-000000000001")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListHoldingDeclarations(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl5", "U", "u@u.com")
	inst1, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "A1", Canonical: false}}, "", nil, nil)
	inst2, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "A2", Canonical: false}}, "", nil, nil)

	asOf := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", inst1, "100", asOf)
	p.CreateHoldingDeclaration(ctx, userID, "IBKR", "acct1", inst2, "200", asOf)

	rows, err := p.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2, got %d", len(rows))
	}
}

func TestGetPortfolioStartDate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl6", "U", "u@u.com")

	// No txs: should return nil
	startDate, err := p.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		t.Fatalf("get start date: %v", err)
	}
	if startDate != nil {
		t.Fatalf("expected nil, got %v", startDate)
	}

	// Add a tx
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SD1", Canonical: false}}, "", nil, nil)
	ts := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	tx := &apiv1.Tx{Timestamp: timestamppb.New(ts), InstrumentDescription: "SD1", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "acct1"}
	p.CreateTx(ctx, userID, "IBKR", "acct1", tx, instID)

	startDate, err = p.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		t.Fatalf("get start date: %v", err)
	}
	if startDate == nil {
		t.Fatal("expected non-nil start date")
	}
	if !startDate.Equal(ts) {
		t.Fatalf("expected %v, got %v", ts, *startDate)
	}
}

func TestComputeRunningBalance(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl7", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "RB1", Canonical: false}}, "", nil, nil)

	ts1 := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	p.CreateTx(ctx, userID, "IBKR", "acct1", &apiv1.Tx{Timestamp: timestamppb.New(ts1), InstrumentDescription: "RB1", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "acct1"}, instID)
	p.CreateTx(ctx, userID, "IBKR", "acct1", &apiv1.Tx{Timestamp: timestamppb.New(ts2), InstrumentDescription: "RB1", Type: apiv1.TxType_SELLSTOCK, Quantity: -30, Account: "acct1"}, instID)

	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	bal, err := p.ComputeRunningBalance(ctx, userID, "IBKR", "acct1", instID, from, to)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if bal != 70 {
		t.Fatalf("expected 70, got %v", bal)
	}
}

func TestUpsertAndDeleteInitializeTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|decl8", "U", "u@u.com")
	instID, _ := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "UI1", Canonical: false}}, "", nil, nil)

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	err := p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, ts, 50)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify it shows up in ListTxs
	txs, _, err := p.ListTxs(ctx, userID, nil, "", nil, nil, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, pt := range txs {
		if pt.GetTx().GetSyntheticPurpose() == "INITIALIZE" {
			found = true
			if pt.GetTx().GetQuantity() != 50 {
				t.Fatalf("expected qty 50, got %v", pt.GetTx().GetQuantity())
			}
		}
	}
	if !found {
		t.Fatal("INITIALIZE tx not found in list")
	}

	// Upsert again with different qty (should update, not duplicate)
	err = p.UpsertInitializeTx(ctx, userID, "IBKR", "acct1", instID, ts, 75)
	if err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	txs, _, _ = p.ListTxs(ctx, userID, nil, "", nil, nil, 50, "")
	var initCount int
	for _, pt := range txs {
		if pt.GetTx().GetSyntheticPurpose() == "INITIALIZE" {
			initCount++
			if pt.GetTx().GetQuantity() != 75 {
				t.Fatalf("expected qty 75 after update, got %v", pt.GetTx().GetQuantity())
			}
		}
	}
	if initCount != 1 {
		t.Fatalf("expected 1 INITIALIZE tx, got %d", initCount)
	}

	// Delete
	err = p.DeleteInitializeTx(ctx, userID, "IBKR", "acct1", instID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	txs, _, _ = p.ListTxs(ctx, userID, nil, "", nil, nil, 50, "")
	for _, pt := range txs {
		if pt.GetTx().GetSyntheticPurpose() == "INITIALIZE" {
			t.Fatal("INITIALIZE tx should have been deleted")
		}
	}
}
