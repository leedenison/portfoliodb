package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetPortfolioValuation_Basic(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val1", "U", "u@val.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort")
	// Filter by broker so portfolio matches txs.
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Create instrument with price data.
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL Corp", Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{instID}); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: 150.0, DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: 155.0, DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Query valuation for Jan 2-3.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateTo, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	// Jan 2: 10 * 150 = 1500
	if points[0].TotalValue != 1500.0 {
		t.Errorf("Jan 2 value: want 1500, got %v", points[0].TotalValue)
	}
	// Jan 3: 10 * 155 = 1550
	if points[1].TotalValue != 1550.0 {
		t.Errorf("Jan 3 value: want 1550, got %v", points[1].TotalValue)
	}
	// No unpriced instruments.
	for _, pt := range points {
		if len(pt.UnpricedInstruments) != 0 {
			t.Errorf("expected no unpriced, got %v on %v", pt.UnpricedInstruments, pt.Date)
		}
	}
}

func TestGetPortfolioValuation_UnpricedInstruments(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val2", "U", "u@val2.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort2")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Insert tx with NULL instrument_id directly (unidentified instrument).
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, instrument_id)
		VALUES ($1, 'IBKR', 'main', $2, 'MYSTERY CORP', 'BUYSTOCK', 5, NULL)
	`, userID, buyDate)
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateTo, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if points[0].TotalValue != 0 {
		t.Errorf("expected 0 total value for unpriced, got %v", points[0].TotalValue)
	}
	found := false
	for _, name := range points[0].UnpricedInstruments {
		if name == "MYSTERY CORP" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MYSTERY CORP in unpriced list, got %v", points[0].UnpricedInstruments)
	}
}

func TestGetPortfolioValuation_MultipleInstruments(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val3", "U", "u@val3.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort3")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Two identified instruments.
	instA, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL multi", Canonical: false},
	}, "", nil, nil)
	instB, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "GOOG", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG multi", Canonical: false},
	}, "", nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL multi", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "main"},
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "GOOG multi", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{instA, instB}); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	prices := []db.EODPrice{
		{InstrumentID: instA, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: 150.0, DataProvider: "test"},
		{InstrumentID: instB, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: 200.0, DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateTo, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// 10*150 + 5*200 = 2500
	if points[0].TotalValue != 2500.0 {
		t.Errorf("want 2500, got %v", points[0].TotalValue)
	}
}

func TestGetUserValuation_Basic(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|uval1", "U", "u@uval.com")

	// Create instrument with price data.
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL UserVal", Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL UserVal", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{instID}); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: 150.0, DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: 155.0, DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Query user valuation (no portfolio) for Jan 2-3.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateTo, "USD")
	if err != nil {
		t.Fatalf("get user valuation: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	// Jan 2: 10 * 150 = 1500
	if points[0].TotalValue != 1500.0 {
		t.Errorf("Jan 2 value: want 1500, got %v", points[0].TotalValue)
	}
	// Jan 3: 10 * 155 = 1550
	if points[1].TotalValue != 1550.0 {
		t.Errorf("Jan 3 value: want 1550, got %v", points[1].TotalValue)
	}
	for _, pt := range points {
		if len(pt.UnpricedInstruments) != 0 {
			t.Errorf("expected no unpriced, got %v on %v", pt.UnpricedInstruments, pt.Date)
		}
	}
}

func TestGetPortfolioValuation_EmptyRange(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val4", "U", "u@val4.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort4")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// No txs at all.
	dateFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateTo, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 points for empty portfolio, got %d", len(points))
	}
}
