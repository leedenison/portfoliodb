package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReplaceTxsInPeriod_and_ComputeHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|tx", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	instrumentIDs := []string{instID, instID}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, instrumentIDs)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, asOf, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10 + -3), got %v", aaplQty)
	}
}

func TestCreateTx_AppendOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|up", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	ts := timestamppb.Now()
	tx1 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""}
	tx2 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx1, instID); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx2, instID); err != nil {
		t.Fatalf("second create: %v", err)
	}
	holdings, _, _ := p.ComputeHoldings(ctx, userID, nil, "", nil)
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" && h.Quantity != 15 {
			t.Fatalf("append-only: expected total quantity 15, got %v", h.Quantity)
		}
	}
}

func TestListTxsByPortfolio_ComputeHoldingsForPortfolio(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pv", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// No filters: portfolio view should return no txs
	txs, tok, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio no filters: %v", err)
	}
	if len(txs) != 0 || tok != "" {
		t.Fatalf("no filters should return 0 txs, got %d %q", len(txs), tok)
	}
	holdings, asOf, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio no filters: %v", err)
	}
	if len(holdings) != 0 || asOf == nil {
		t.Fatalf("no filters holdings: %v asOf %v", holdings, asOf)
	}
	// Add broker=IBKR filter
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txList := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txList, []string{instID, instID}); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	txs, tok, err = p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 2 || tok != "" {
		t.Fatalf("expected 2 txs, got %d nextToken=%q", len(txs), tok)
	}
	holdings, asOf, err = p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10-3), got %v", aaplQty)
	}
}

// TestListTxsByPortfolio_OR_dedupe verifies OR semantics and deduplication: a tx matching multiple filters appears once.
func TestListTxsByPortfolio_OR_dedupe(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|dedupe", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// Filters: broker=IBKR OR account=A (so txs matching either appear; tx matching both appears once)
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
		{FilterType: "account", FilterValue: "A"},
	}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	now := time.Now()
	ts := timestamppb.New(now.Add(-1 * time.Hour))
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "X", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	// Tx1: IBKR, account "" -> matches broker only
	// Tx2: SCHB, account "A" -> matches account only
	// Tx3: IBKR, account "A" -> matches both, should appear once
	if err := p.CreateTx(ctx, userID, "IBKR", "", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: ""}, instID); err != nil {
		t.Fatalf("create tx1: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "SCHB", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 2, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx2: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 3, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx3: %v", err)
	}
	txs, _, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("expected 3 txs (OR + dedupe), got %d: %+v", len(txs), txs)
	}
	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	// One instrument, total quantity 1+2+3 = 6 (each tx counted once)
	var totalQty float64
	for _, h := range holdings {
		totalQty += h.Quantity
	}
	if totalQty != 6 {
		t.Fatalf("expected total quantity 6 (deduped txs), got %v", totalQty)
	}
}
