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
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL Corp", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Query valuation for Jan 2-3.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
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
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, instrument_id,
		                 weight, weight_commodity, group_id)
		VALUES ($1, 'IBKR', 'main', $2, 'MYSTERY CORP', 'BUYSTOCK', 5, NULL, 5, 'desc:MYSTERY CORP', $3::uuid)
	`, userID, buyDate, newTxGroup(t, p, userID))
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
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

// TestGetPortfolioValuation_DifferentDescriptionsNetToZero verifies that
// transactions for the same instrument_id but different instrument_descriptions
// (e.g. TRANSFER "ABNB" +213 and SELLSTOCK "ABNB AIRBNB INC-CLASS A" -213)
// net to zero and do not appear in the valuation or unpriced list.
func TestGetPortfolioValuation_DifferentDescriptionsNetToZero(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-net0", "U", "u@val-net0.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPortNet0")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "ABNB", Canonical: true},
	}, "", nil, nil, nil)

	transferDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	sellDate := time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)

	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(transferDate), InstrumentDescription: "ABNB", Type: apiv1.TxType_TRANSFER, Quantity: "213", Account: "main"},
		{Timestamp: timestamppb.New(sellDate), InstrumentDescription: "ABNB AIRBNB INC-CLASS A", Type: apiv1.TxType_SELLSTOCK, Quantity: "-213", Account: "main"},
	}
	from := timestamppb.New(transferDate.Add(-1 * time.Hour))
	to := timestamppb.New(sellDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Add a price so the holding period (Jan 2-4) is valued.
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(101.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC), Close: decf(102.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC), Close: decf(103.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Query range spanning the sell date — after Jan 5, position is zero.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 7, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}

	// Jan 6 should either be absent (zero position produces no row) or have zero value.
	for _, pt := range points {
		if pt.Date.Equal(time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)) {
			if pt.TotalValue != 0 {
				t.Errorf("Jan 6: expected 0 total value, got %v", pt.TotalValue)
			}
			if len(pt.UnpricedInstruments) != 0 {
				t.Errorf("Jan 6: expected no unpriced instruments, got %v", pt.UnpricedInstruments)
			}
		}
	}
	// No day should show ABNB as unpriced (it has prices for the entire holding period).
	for _, pt := range points {
		for _, name := range pt.UnpricedInstruments {
			if name == "ABNB" {
				t.Errorf("%v: ABNB should not appear as unpriced", pt.Date)
			}
		}
	}
}

// TestGetPortfolioValuation_UnpricedDeduplication verifies that two transactions
// with different instrument_descriptions but the same instrument_id produce a
// single entry in unpriced_instruments (using the canonical instrument name).
func TestGetPortfolioValuation_UnpricedDeduplication(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-dedup", "U", "u@val-dedup.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPortDedup")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Create an instrument with a canonical name (from MIC_TICKER) but no prices.
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "ABNB", Canonical: true},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)

	// Two txs for the same instrument but with different descriptions.
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "ABNB", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "ABNB AIRBNB INC-CLASS A", Type: apiv1.TxType_BUYSTOCK, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}

	// Should have exactly one unpriced entry using the canonical name "ABNB".
	unpriced := points[0].UnpricedInstruments
	if len(unpriced) != 1 {
		t.Errorf("expected 1 unpriced instrument (deduplicated), got %d: %v", len(unpriced), unpriced)
	}
	if len(unpriced) > 0 && unpriced[0] != "ABNB" {
		t.Errorf("expected unpriced instrument name 'ABNB', got %q", unpriced[0])
	}
}

func TestGetPortfolioValuation_MultipleInstruments(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val3", "U", "u@val3.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort3")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Two identified instruments.
	instA, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL multi", Canonical: false},
	}, "", nil, nil, nil)
	instB, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "GOOG", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "GOOG multi", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL multi", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "GOOG multi", Type: apiv1.TxType_BUYSTOCK, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instA, instB}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	prices := []db.EODPrice{
		{InstrumentID: instA, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{InstrumentID: instB, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
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
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL UserVal", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL UserVal", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Query user valuation (no portfolio) for Jan 2-3.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
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
	dateBefore := time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("expected 0 points for empty portfolio, got %d", len(points))
	}
}

func TestGetPortfolioValuation_ExcludesDateBefore(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val5", "U", "u@val5.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort5")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL Corp", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)), txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// The upper bound is exclusive, so Jan 3 is not valued.
	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if !points[0].Date.Equal(dateFrom) {
		t.Errorf("point dated %v, want %v", points[0].Date, dateFrom)
	}
}

func TestGetPortfolioValuation_FromEqualsBeforeReturnsNothing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val6", "U", "u@val6.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort6")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL Corp", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)), txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	day := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	points, err := p.GetPortfolioValuation(ctx, port.Id, day, day, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("empty range should value nothing, got %d points", len(points))
	}
}

// lookupFXInstrumentVal finds the FX pair instrument ID for a given currency.
func lookupFXInstrumentVal(t *testing.T, p *Postgres, currency string) string {
	t.Helper()
	ctx := context.Background()
	id, err := p.FindInstrumentByTypeAndValue(ctx, "FX_PAIR", currency+"USD")
	if err != nil {
		t.Fatalf("lookup FX instrument for %s: %v", currency, err)
	}
	if id == "" {
		t.Fatalf("no FX instrument found for %sUSD", currency)
	}
	return id
}

func TestGetUserValuation_FXConversion_DisplayUSD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxval1", "U", "u@fxval1.com")

	// Create a EUR-denominated instrument.
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SAP FX", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "SAP FX", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EUR price (in EUR).
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD FX rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: eurFX, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert fx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// 10 shares * 200 EUR * 1.08 USD/EUR = 2160 USD
	expected := 10 * 200.0 * 1.08
	if diff := points[0].TotalValue - expected; diff < -0.01 || diff > 0.01 {
		t.Errorf("total value: want %.2f, got %.2f", expected, points[0].TotalValue)
	}
	if len(points[0].UnpricedInstruments) != 0 {
		t.Errorf("expected no unpriced, got %v", points[0].UnpricedInstruments)
	}
}

func TestGetUserValuation_FXConversion_CrossRate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxval2", "U", "u@fxval2.com")

	// Create a GBP-denominated instrument.
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "HSBC", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "HSBC FX", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "HSBC FX", Type: apiv1.TxType_BUYSTOCK, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert GBP price.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert GBP/USD and EUR/USD rates.
	gbpFX := lookupFXInstrumentVal(t, p, "GBP")
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: gbpFX, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
		{InstrumentID: eurFX, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert fx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	// Display in EUR: value = 5 * 100 GBP * (1.27 GBPUSD / 1.08 EURUSD) = 587.96 EUR
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "EUR")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	expected := 5 * 100.0 * (1.27 / 1.08)
	if diff := points[0].TotalValue - expected; diff < -0.01 || diff > 0.01 {
		t.Errorf("total value: want %.2f, got %.2f", expected, points[0].TotalValue)
	}
}

func TestGetUserValuation_FXConversion_MissingRate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxval3", "U", "u@fxval3.com")

	// Create a EUR-denominated instrument (no FX rate will be inserted).
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP-NR", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SAP NR", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "SAP NR", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert instrument price but NO FX rate.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// Missing FX rate: value should be 0 and instrument should be unpriced.
	if points[0].TotalValue != 0 {
		t.Errorf("total value: want 0, got %v", points[0].TotalValue)
	}
	found := false
	for _, name := range points[0].UnpricedInstruments {
		if name == "SAP NR" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SAP NR in unpriced, got %v", points[0].UnpricedInstruments)
	}
}

func TestGetUserValuation_FXConversion_USDDisplayNonUSD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxval4", "U", "u@fxval4.com")

	// USD-denominated instrument displayed in EUR.
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL-FXD", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL FXD", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL FXD", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: eurFX, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert fx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	// Display EUR: USD instrument => fx_rate = 1.0 / 1.08 = 0.9259...
	// value = 10 * 150 * (1.0 / 1.08) = 1388.89 EUR
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "EUR")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	expected := 10 * 150.0 / 1.08
	if diff := points[0].TotalValue - expected; diff < -0.01 || diff > 0.01 {
		t.Errorf("total value: want %.2f, got %.2f", expected, points[0].TotalValue)
	}
}

func TestGetUserValuation_FXConversion_MissingBaseRate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxval5", "U", "u@fxval5.com")

	// GBP instrument displayed in EUR. GBP/USD rate is MISSING, EUR/USD is present.
	// The base rate (GBPUSD) is needed for the cross-rate but absent.
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "HSBC-MBR", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "HSBC MBR", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "HSBC MBR", Type: apiv1.TxType_BUYSTOCK, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert GBP instrument price.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD rate but NOT GBP/USD rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: eurFX, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert fx: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "EUR")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// Missing GBP/USD base rate: instrument should be unpriced, value = 0.
	if points[0].TotalValue != 0 {
		t.Errorf("total value: want 0, got %v", points[0].TotalValue)
	}
	found := false
	for _, name := range points[0].UnpricedInstruments {
		if name == "HSBC MBR" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HSBC MBR in unpriced, got %v", points[0].UnpricedInstruments)
	}
}

// TestGetUserValuation_CashInDisplayCurrency verifies that a USD cash holding
// with USD display currency is valued at qty (implicit price 1.0) and does
// NOT appear in the unpriced instruments list.
func TestGetUserValuation_CashInDisplayCurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-cash-usd", "U", "u@cash-usd.com")

	// Look up the seeded USD cash instrument.
	usdInstID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "USD")
	if err != nil || usdInstID == "" {
		t.Fatalf("USD cash instrument not found: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "USD CASH", Type: apiv1.TxType_INCOME, Quantity: "500", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{usdInstID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if points[0].TotalValue != 500 {
		t.Errorf("total value: want 500, got %v", points[0].TotalValue)
	}
	if len(points[0].UnpricedInstruments) != 0 {
		t.Errorf("expected no unpriced instruments, got %v", points[0].UnpricedInstruments)
	}
}

// TestGetUserValuation_CashInForeignCurrency verifies that a GBP cash holding
// with USD display currency is valued at qty * GBPUSD rate and does NOT appear
// in the unpriced instruments list when the FX rate is available.
func TestGetUserValuation_CashInForeignCurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-cash-gbp", "U", "u@cash-gbp.com")

	gbpInstID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "GBP")
	if err != nil || gbpInstID == "" {
		t.Fatalf("GBP cash instrument not found: %v", err)
	}

	// Create GBPUSD FX pair instrument and price.
	fxInstID, _ := p.EnsureInstrument(ctx, "FX", "", "", "", "", "", []db.IdentifierInput{
		{Type: "FX_PAIR", Domain: "", Value: "GBPUSD", Canonical: true},
	}, "", nil, nil, nil)
	fxPrices := []db.EODPrice{
		{InstrumentID: fxInstID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, fxPrices); err != nil {
		t.Fatalf("upsert fx prices: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "GBP CASH", Type: apiv1.TxType_INCOME, Quantity: "1000", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{gbpInstID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// 1000 GBP * 1.27 GBPUSD = 1270 USD
	if points[0].TotalValue != 1270 {
		t.Errorf("total value: want 1270, got %v", points[0].TotalValue)
	}
	if len(points[0].UnpricedInstruments) != 0 {
		t.Errorf("expected no unpriced instruments, got %v", points[0].UnpricedInstruments)
	}
}

// TestGetUserValuation_CashForeignMissingFXRate verifies that a GBP cash
// holding with USD display currency and no GBPUSD FX rate shows as unpriced.
func TestGetUserValuation_CashForeignMissingFXRate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-cash-nofx", "U", "u@cash-nofx.com")

	gbpInstID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "GBP")
	if err != nil || gbpInstID == "" {
		t.Fatalf("GBP cash instrument not found: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "GBP CASH", Type: apiv1.TxType_INCOME, Quantity: "1000", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{gbpInstID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if points[0].TotalValue != 0 {
		t.Errorf("total value: want 0 (missing FX rate), got %v", points[0].TotalValue)
	}
	found := false
	for _, name := range points[0].UnpricedInstruments {
		if name == "GBP" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GBP in unpriced (missing FX rate), got %v", points[0].UnpricedInstruments)
	}
}

// TestGetUserValuation_CashForeignCurrency_NonUSDDisplay verifies the cross-rate
// path: EUR cash displayed in GBP requires both EURUSD and GBPUSD rates.
func TestGetUserValuation_CashForeignCurrency_NonUSDDisplay(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-cash-cross", "U", "u@cash-cross.com")

	eurInstID, err := p.FindInstrumentByIdentifier(ctx, "CURRENCY", "", "EUR")
	if err != nil || eurInstID == "" {
		t.Fatalf("EUR cash instrument not found: %v", err)
	}

	// Create EURUSD and GBPUSD FX pair instruments and prices.
	eurFxID, _ := p.EnsureInstrument(ctx, "FX", "", "", "", "", "", []db.IdentifierInput{
		{Type: "FX_PAIR", Domain: "", Value: "EURUSD", Canonical: true},
	}, "", nil, nil, nil)
	gbpFxID, _ := p.EnsureInstrument(ctx, "FX", "", "", "", "", "", []db.IdentifierInput{
		{Type: "FX_PAIR", Domain: "", Value: "GBPUSD", Canonical: true},
	}, "", nil, nil, nil)
	fxPrices := []db.EODPrice{
		{InstrumentID: eurFxID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
		{InstrumentID: gbpFxID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, fxPrices); err != nil {
		t.Fatalf("upsert fx prices: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "EUR CASH", Type: apiv1.TxType_INCOME, Quantity: "1000", TradingCurrency: "EUR", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{eurInstID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	dateFrom := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	dateBefore := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	// Display in GBP: value = 1000 EUR * (EURUSD / GBPUSD) = 1000 * 1.08 / 1.27
	points, err := p.GetUserValuation(ctx, userID, dateFrom, dateBefore, "GBP")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	// 1000 * 1.08 / 1.27 ≈ 850.39
	want := 1000.0 * 1.08 / 1.27
	if diff := points[0].TotalValue - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("total value: want ~%.2f, got %v", want, points[0].TotalValue)
	}
	if len(points[0].UnpricedInstruments) != 0 {
		t.Errorf("expected no unpriced instruments, got %v", points[0].UnpricedInstruments)
	}
}

// TestGetUserValuation_ContinuousAcrossSplit is the invariant that quantity and
// price must be paired on the same share count. A holding untouched across a
// 4:1 split is worth the same on both sides of the ex-date: the share count
// quadruples and the per-share price quarters.
//
// Both series must be adjusted or neither. Raw quantity never steps up, because
// TX_TYPE=SPLIT rows are dropped at ingestion and corporate events are shared
// reference data (see adr/0005-corporate-events-design.md), while the as-traded
// price does step down. Pairing them shows a cliff at the ex-date.
func TestGetUserValuation_ContinuousAcrossSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|split1", "U", "u@split.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "AAPL Split", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Buy 100 shares well before the split, then leave the position alone.
	buyDate := time.Date(2020, 8, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "AAPL Split", Type: apiv1.TxType_BUYSTOCK, Quantity: "100", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID,
		"IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// As-traded closes either side of a 4:1 split: ~500 before, ~125 after.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: d(2020, 8, 28), Close: decf(500), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: d(2020, 8, 31), Close: decf(125), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID, d(2020, 8, 28), d(2020, 9, 1), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	before, after := valueOn(t, points, d(2020, 8, 28)), valueOn(t, points, d(2020, 8, 31))
	if !approxEq(before, 50_000) {
		t.Errorf("value before the split: got %v want %v", before, 50_000.0)
	}
	if !approxEq(after, 50_000) {
		t.Errorf("value after the split: got %v want %v (a %.0f%% cliff at the ex-date)",
			after, 50_000.0, (1-after/before)*100)
	}
}

func valueOn(t *testing.T, points []db.ValuationPoint, want time.Time) float64 {
	t.Helper()
	for _, pt := range points {
		if pt.Date.Equal(want) {
			return pt.TotalValue
		}
	}
	t.Fatalf("no valuation point for %s", want.Format("2006-01-02"))
	return 0
}

// TestGetUserValuation_FXUnaffectedByASplit guards the FX leg against the
// share-count change. An exchange rate is not denominated in a share count, so
// the split must reach the holding and the price and leave the rate alone.
func TestGetUserValuation_FXUnaffectedByASplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|fxsplit", "U", "u@fxsplit.com")
	instID, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "SAP FXSplit", Canonical: false},
	}, "", nil, nil, nil)

	buyDate := time.Date(2020, 8, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "SAP FXSplit", Type: apiv1.TxType_BUYSTOCK, Quantity: "100", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{InstrumentID: instID, PriceDate: d(2020, 8, 28), Close: decf(500), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: d(2020, 8, 31), Close: decf(125), DataProvider: "test"},
		// A flat rate across both days: any movement in the result is the split.
		{InstrumentID: eurFX, PriceDate: d(2020, 8, 28), Close: decf(1.2), DataProvider: "test"},
		{InstrumentID: eurFX, PriceDate: d(2020, 8, 31), Close: decf(1.2), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID, d(2020, 8, 28), d(2020, 9, 1), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	// 100 shares * 500 EUR * 1.2 USD/EUR = 60000 USD, on both days.
	for _, day := range []time.Time{d(2020, 8, 28), d(2020, 8, 31)} {
		if got := valueOn(t, points, day); !approxEq(got, 60_000) {
			t.Errorf("value on %s: got %v want %v", day.Format("2006-01-02"), got, 60_000.0)
		}
	}
}

// setupHeldInstrument creates a user holding one instrument from buyDate on.
func setupHeldInstrument(t *testing.T, p *Postgres, sub, desc string, qty string, buyDate time.Time) (string, string) {
	t.Helper()
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, sub, "U", sub+"@locf.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", desc, "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: desc, Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	ts := time.Date(buyDate.Year(), buyDate.Month(), buyDate.Day(), 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(ts), InstrumentDescription: desc, Type: apiv1.TxType_BUYSTOCK, Quantity: qty, Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(ts.Add(-time.Hour)), timestamppb.New(ts.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	return userID, instID
}

// Weekends have no stored row now, so the close must be carried forward at read
// time or every Saturday would read as unpriced.
func TestGetUserValuation_CarriesForwardOverNonTradingDays(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := setupHeldInstrument(t, p, "sub|locf1", "LOCF1", "10", d(2024, 1, 1))

	// Fri 5 Jan is the last bar; Sat and Sun have none.
	bars := []db.EODPrice{{InstrumentID: instID, PriceDate: d(2024, 1, 5), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, instID, "test", bars, d(2024, 1, 5), d(2024, 1, 8), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID, d(2024, 1, 5), d(2024, 1, 8), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	for _, day := range []time.Time{d(2024, 1, 5), d(2024, 1, 6), d(2024, 1, 7)} {
		if got := valueOn(t, points, day); !approxEq(got, 1000) {
			t.Errorf("%s: got %v want 1000", day.Format("2006-01-02"), got)
		}
	}
}

// Carry-forward stops at the end of the covered span. Without that bound a
// delisted instrument would hold its last close for ever.
func TestGetUserValuation_CarryForwardStopsAtCoverageEnd(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := setupHeldInstrument(t, p, "sub|locf2", "LOCF2", "10", d(2024, 1, 1))

	bars := []db.EODPrice{{InstrumentID: instID, PriceDate: d(2024, 1, 5), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, instID, "test", bars, d(2024, 1, 5), d(2024, 1, 7), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID, d(2024, 1, 5), d(2024, 1, 9), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	if got := valueOn(t, points, d(2024, 1, 6)); !approxEq(got, 1000) {
		t.Errorf("inside coverage: got %v want 1000", got)
	}
	// 7 Jan is past covered_before, so the position is unpriced, not stale.
	if got := valueOn(t, points, d(2024, 1, 7)); !approxEq(got, 0) {
		t.Errorf("past coverage end: got %v want 0 (unpriced, not carried)", got)
	}
	if !unpricedOn(t, points, d(2024, 1, 7)) {
		t.Error("past coverage end: expected the instrument to be reported unpriced")
	}
}

// Two disjoint covered periods must not bleed into each other: the first
// period's close is not a price for a day in the gap or in the second period
// before its own first bar.
func TestGetUserValuation_DisjointCoverageDoesNotBleed(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := setupHeldInstrument(t, p, "sub|locf3", "LOCF3", "10", d(2024, 1, 1))

	first := []db.EODPrice{{InstrumentID: instID, PriceDate: d(2024, 1, 2), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, instID, "test", first, d(2024, 1, 2), d(2024, 1, 4), nil); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	second := []db.EODPrice{{InstrumentID: instID, PriceDate: d(2024, 1, 9), Close: decf(200)}}
	if err := p.UpsertPricesForRange(ctx, instID, "test", second, d(2024, 1, 8), d(2024, 1, 10), nil); err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID, d(2024, 1, 2), d(2024, 1, 10), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	if got := valueOn(t, points, d(2024, 1, 3)); !approxEq(got, 1000) {
		t.Errorf("inside first span: got %v want 1000", got)
	}
	// In the uncovered gap, and on the second span's first day which precedes
	// its own first bar: neither may inherit the first span's close.
	for _, day := range []time.Time{d(2024, 1, 5), d(2024, 1, 8)} {
		if got := valueOn(t, points, day); !approxEq(got, 0) {
			t.Errorf("%s: got %v want 0 (must not inherit the earlier span's close)",
				day.Format("2006-01-02"), got)
		}
	}
	if got := valueOn(t, points, d(2024, 1, 9)); !approxEq(got, 2000) {
		t.Errorf("inside second span: got %v want 2000", got)
	}
}

// A window opening mid-span must pick up the last bar before it, or the chart
// would show the position unpriced until the next bar lands.
func TestGetUserValuation_SeedsFromBarBeforeWindow(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := setupHeldInstrument(t, p, "sub|locf4", "LOCF4", "10", d(2024, 1, 1))

	bars := []db.EODPrice{{InstrumentID: instID, PriceDate: d(2024, 1, 2), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, instID, "test", bars, d(2024, 1, 1), d(2024, 1, 20), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	// The window starts well after the only bar, which is still its price.
	points, err := p.GetUserValuation(ctx, userID, d(2024, 1, 10), d(2024, 1, 12), "USD")
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	if got := valueOn(t, points, d(2024, 1, 10)); !approxEq(got, 1000) {
		t.Errorf("window opening mid-span: got %v want 1000", got)
	}
}

func unpricedOn(t *testing.T, points []db.ValuationPoint, want time.Time) bool {
	t.Helper()
	for _, pt := range points {
		if pt.Date.Equal(want) {
			return len(pt.UnpricedInstruments) > 0
		}
	}
	t.Fatalf("no valuation point for %s", want.Format("2006-01-02"))
	return false
}

// TestGetUserValuation_ExcludesDatesBeforeFirstTx covers qty_is_zero's NULL
// branch, which is the only reason the function still exists now that quantities
// are exact and there is no residual to absorb. daily_holdings forward-fills the
// last known position with a LIMIT 1 correlated subquery, so on every grid day
// before an instrument's first tx the position is NULL rather than zero. The NULL
// branch classifies that as "not held", so the row leaves the valued CTE and the
// day drops out of the series entirely -- priced days before the first purchase
// are absent rather than zero, and the instrument is never reported as unpriced
// on them.
func TestGetUserValuation_ExcludesDatesBeforeFirstTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|nullqty", "U", "u@nullqty.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "NULLQ", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "IBKR", Value: "NULLQ", Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Bought on Jan 3, but the valuation window opens on Jan 1.
	buyDate := time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(buyDate), InstrumentDescription: "NULLQ", Type: apiv1.TxType_BUYSTOCK, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	prices := []db.EODPrice{
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{InstrumentID: instID, PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID,
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("get user valuation: %v", err)
	}
	// Jan 1 and Jan 2 are priced but not held, so they are not in the series at
	// all. Only Jan 3 survives.
	if len(points) != 1 {
		t.Fatalf("expected 1 point, got %d", len(points))
	}
	if got := points[0].Date.Format("2006-01-02"); got != "2025-01-03" {
		t.Errorf("point date: got %s want 2025-01-03", got)
	}
	if points[0].TotalValue != 1500 {
		t.Errorf("total value: got %v want 1500", points[0].TotalValue)
	}
	// A NULL position is not an unpriced one: the instrument simply was not held.
	if len(points[0].UnpricedInstruments) != 0 {
		t.Errorf("expected no unpriced instruments, got %v", points[0].UnpricedInstruments)
	}
}
