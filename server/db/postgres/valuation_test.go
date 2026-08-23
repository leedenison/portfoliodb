package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

func TestGetPortfolioValuation_Basic(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val1", "U", "u@val.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPort")
	// Filter by broker so portfolio matches txs.
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	// Create instrument with price data.
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Corp", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
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
		INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
		                 broker_tx_type, resolved_tx_type, quantity, instrument_id,
		                 weight, weight_commodity, group_id)
		VALUES ($1, 'IBKR', 'main', $2, $2, 'MYSTERY CORP', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 5, NULL, 5, 'desc:MYSTERY CORP', $3::uuid)
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
// (e.g. TRANSFER "ABNB" +213 and TRADE_ASSET "ABNB AIRBNB INC-CLASS A" -213)
// net to zero and do not appear in the valuation or unpriced list.
func TestGetPortfolioValuation_DifferentDescriptionsNetToZero(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-net0", "U", "u@val-net0.com")
	port, _ := p.CreatePortfolio(ctx, userID, "ValPortNet0")
	_ = p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}})

	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "ABNB", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)

	transferDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	sellDate := time.Date(2025, 1, 5, 12, 0, 0, 0, time.UTC)

	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(transferDate),
			TradeDate: timestamppb.New(transferDate), InstrumentDescription: "ABNB", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER, Quantity: "213", Account: "main"},
		{OrderDate: timestamppb.New(sellDate),
			TradeDate: timestamppb.New(sellDate), InstrumentDescription: "ABNB AIRBNB INC-CLASS A", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-213", Account: "main"},
	}
	from := timestamppb.New(transferDate.Add(-1 * time.Hour))
	to := timestamppb.New(sellDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID, instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Add a price so the holding period (Jan 2-4) is valued.
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(101.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC), Close: decf(102.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC), Close: decf(103.0), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "ABNB", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)

	// Two txs for the same instrument but with different descriptions.
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "ABNB", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "ABNB AIRBNB INC-CLASS A", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "main"},
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
	instA, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL multi", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	instB, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "GOOG", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "GOOG multi", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL multi", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "GOOG multi", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instA, instB}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instA), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instB), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
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
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL UserVal", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Insert a buy of 10 shares on Jan 2.
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL UserVal", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EOD prices for Jan 2 and Jan 3.
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
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

	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Corp", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)), txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(155.0), DataProvider: "test"},
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

	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Corp", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL Corp", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)), txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SAP FX", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "SAP FX", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert EUR price (in EUR).
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD FX rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, eurFX), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "HSBC", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "HSBC FX", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "HSBC FX", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert GBP price.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert GBP/USD and EUR/USD rates.
	gbpFX := lookupFXInstrumentVal(t, p, "GBP")
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, gbpFX), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
		{ListingID: pricedListing(t, p, eurFX), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP-NR", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SAP NR", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "SAP NR", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert instrument price but NO FX rate.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(200.0), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL-FXD", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL FXD", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL FXD", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, eurFX), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "GBP", "HSBC-MBR", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "HSBC MBR", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "HSBC MBR", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "main"},
	}
	from := timestamppb.New(buyDate.Add(-1 * time.Hour))
	to := timestamppb.New(buyDate.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// Insert GBP instrument price.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	// Insert EUR/USD rate but NOT GBP/USD rate.
	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, eurFX), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
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
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "USD CASH", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME, Quantity: "500", Account: "main"},
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
	fxInstID, _, _ := p.EnsureInstrument(ctx, "FX", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "FX_PAIR", Value: "GBPUSD", Domain: ""},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	fxPrices := []db.EODPrice{
		{ListingID: pricedListing(t, p, fxInstID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, fxPrices); err != nil {
		t.Fatalf("upsert fx prices: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "GBP CASH", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME, Quantity: "1000", Account: "main"},
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
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "GBP CASH", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME, Quantity: "1000", Account: "main"},
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
	eurFxID, _, _ := p.EnsureInstrument(ctx, "FX", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "FX_PAIR", Value: "EURUSD", Domain: ""},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	gbpFxID, _, _ := p.EnsureInstrument(ctx, "FX", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "FX_PAIR", Value: "GBPUSD", Domain: ""},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	fxPrices := []db.EODPrice{
		{ListingID: pricedListing(t, p, eurFxID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.08), DataProvider: "test"},
		{ListingID: pricedListing(t, p, gbpFxID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(1.27), DataProvider: "test"},
	}
	if err := p.UpsertPrices(ctx, fxPrices); err != nil {
		t.Fatalf("upsert fx prices: %v", err)
	}

	buyDate := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "EUR CASH", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME, Quantity: "1000", TradingCurrency: "EUR", Account: "main"},
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
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Split", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Buy 100 shares well before the split, then leave the position alone.
	buyDate := time.Date(2020, 8, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "AAPL Split", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID,
		"IBKR", "", timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	// As-traded closes either side of a 4:1 split: ~500 before, ~125 after.
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2020, 8, 28), Close: decf(500), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2020, 8, 31), Close: decf(125), DataProvider: "test"},
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
	instID, _, _ := p.EnsureInstrument(ctx, "STOCK", "", "EUR", "SAP", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SAP FXSplit", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)

	buyDate := time.Date(2020, 8, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "SAP FXSplit", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}

	eurFX := lookupFXInstrumentVal(t, p, "EUR")
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2020, 8, 28), Close: decf(500), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2020, 8, 31), Close: decf(125), DataProvider: "test"},
		// A flat rate across both days: any movement in the result is the split.
		{ListingID: pricedListing(t, p, eurFX), PriceDate: d(2020, 8, 28), Close: decf(1.2), DataProvider: "test"},
		{ListingID: pricedListing(t, p, eurFX), PriceDate: d(2020, 8, 31), Close: decf(1.2), DataProvider: "test"},
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
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", desc, "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	ts := time.Date(buyDate.Year(), buyDate.Month(), buyDate.Day(), 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(ts),
			TradeDate: timestamppb.New(ts), InstrumentDescription: desc, BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: qty, Account: "main"},
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
	bars := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 5), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", bars, d(2024, 1, 5), d(2024, 1, 8), nil); err != nil {
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

	bars := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 5), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", bars, d(2024, 1, 5), d(2024, 1, 7), nil); err != nil {
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

	first := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 2), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", first, d(2024, 1, 2), d(2024, 1, 4), nil); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	second := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 9), Close: decf(200)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", second, d(2024, 1, 8), d(2024, 1, 10), nil); err != nil {
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

	bars := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 2), Close: decf(100)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", bars, d(2024, 1, 1), d(2024, 1, 20), nil); err != nil {
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

// TestGetUserValuation_ExcludesDatesBeforeFirstTx pins what a date before an
// instrument's first transaction is worth. daily_holdings joins each running
// total to the days its span covers, and a span only opens at a transaction, so
// such a date has no row at all and never reaches valued -- priced days before
// the first purchase are absent from the series rather than zero, and the
// instrument is never reported as unpriced on them.
//
// It also guards qty_is_zero's NULL branch, which is the other way of arriving
// at the same answer: daily_holdings once produced a row with a NULL position
// for those days and left it to qty_is_zero to discard. Either shape has to
// agree with this test, which is why it is written against the series rather
// than against how the series is built.
func TestGetUserValuation_ExcludesDatesBeforeFirstTx(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|nullqty", "U", "u@nullqty.com")
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "NULLQ", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "NULLQ", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	// Bought on Jan 3, but the valuation window opens on Jan 1.
	buyDate := time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC)
	txs := []*apiv1.Tx{
		{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "NULLQ", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "main"},
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", "",
		timestamppb.New(buyDate.Add(-time.Hour)), timestamppb.New(buyDate.Add(time.Hour)),
		txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC), Close: decf(100.0), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC), Close: decf(150.0), DataProvider: "test"},
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

// TestGetUserValuation_ExcludesDatesAfterCloseAcrossInexactSplit covers the other
// branch of the same test: the position is closed, but a reverse split converted its
// postings by a third and each rounded at the split-adjusted columns' declared
// scale, so the running sum lands 1e-12 from zero rather than on it. Tested against
// exact zero the instrument stays "held" forever and every later grid day is valued
// at a fraction of a share; bounded by the roundings that could have produced it,
// the series ends where the position did.
func TestGetUserValuation_ExcludesDatesAfterCloseAcrossInexactSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|closedsplit", "U", "u@closedsplit.com")
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "REVQ", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "REVQ", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	buyDate := time.Date(2025, 1, 3, 12, 0, 0, 0, time.UTC)
	sellDate := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	addSplit(t, p, instID, time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC), 3, 1)

	// Two buys of 10 pre-split are 3.333333333333 each afterwards; the
	// 6.666666666667 the broker then sold is what the position actually was.
	for _, q := range []string{"10", "10"} {
		buy := &apiv1.Tx{OrderDate: timestamppb.New(buyDate),
			TradeDate: timestamppb.New(buyDate), InstrumentDescription: "REVQ",
			BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: q, Account: "main"}
		if err := createTx(ctx, p, userID, "IBKR", "main", "", buy, instID, nil); err != nil {
			t.Fatalf("create buy: %v", err)
		}
	}
	sell := &apiv1.Tx{OrderDate: timestamppb.New(sellDate),
		TradeDate: timestamppb.New(sellDate), InstrumentDescription: "REVQ",
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-6.666666666667", Account: "main"}
	if err := createTx(ctx, p, userID, "IBKR", "main", "", sell, instID, nil); err != nil {
		t.Fatalf("create sell: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute split adjustments: %v", err)
	}

	var prices []db.EODPrice
	for d := 3; d <= 9; d++ {
		prices = append(prices, db.EODPrice{
			ListingID:    pricedListing(t, p, instID),
			PriceDate:    time.Date(2025, 1, d, 0, 0, 0, 0, time.UTC),
			Close:        decf(100.0),
			DataProvider: "test",
		})
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	points, err := p.GetUserValuation(ctx, userID,
		time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("get user valuation: %v", err)
	}
	// Held from the buy up to the day before the sell, and nothing after it.
	var dates []string
	for _, pt := range points {
		dates = append(dates, pt.Date.Format("2006-01-02"))
	}
	want := []string{"2025-01-03", "2025-01-04", "2025-01-05", "2025-01-06"}
	if len(dates) != len(want) {
		t.Fatalf("valued dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("valued dates = %v, want %v", dates, want)
		}
	}
}

// The tests below cover in-flight value: the TRANSFER_CLEARING legs of a matched
// pair, which valuation admits and holdings do not. See portfolio_in_flight_txs in
// server/migrations/001_initial.sql.

// dailyValues indexes a valuation series by date. A day on which a portfolio holds
// nothing produces no point at all rather than a zero, and the dip these tests are
// about is exactly such a day, so asserting on it means asking for a date the series
// may not carry.
func dailyValues(points []db.ValuationPoint) map[string]float64 {
	out := make(map[string]float64, len(points))
	for _, pt := range points {
		out[pt.Date.Format("2006-01-02")] = pt.TotalValue
	}
	return out
}

// inFlightSpec is the transfer these tests move: 20,000 US dollars leaving one
// Fidelity account on 15 April and arriving in another on the 20th. The commodity is
// the seeded USD cash instrument and the display currency is USD throughout, so a
// value equals a quantity and no price or FX fixture is needed.
func inFlightSpec(t *testing.T, p *Postgres, depart, arrive time.Time) transferSpec {
	t.Helper()
	usdInstID, err := p.FindInstrumentByIdentifier(context.Background(), "CURRENCY", "", "USD")
	if err != nil || usdInstID == "" {
		t.Fatalf("USD cash instrument not found: %v", err)
	}
	return transferSpec{
		instID: usdInstID, desc: "USD CASH", qty: "-20000",
		depart: depart, arrive: arrive,
		fromAcct: "AG10000001", toAcct: "AW10000001",
	}
}

// openingCash seeds the balance a transfer moves, dated before the transfer so that
// the fixture's own replacement period does not take it back out again.
func openingCash(t *testing.T, p *Postgres, userID, instID, account string, at time.Time, qty string) {
	t.Helper()
	ctx := context.Background()
	txs := []*apiv1.Tx{{
		OrderDate: timestamppb.New(at),
		TradeDate: timestamppb.New(at), InstrumentDescription: "USD CASH",
		BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, ResolvedTxType: typev1.TxType_INCOME,
		Quantity: qty, Account: account,
	}}
	from := timestamppb.New(at.Add(-1 * time.Hour))
	to := timestamppb.New(at.Add(1 * time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", "", from, to, txs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("opening cash: %v", err)
	}
}

// accountPortfolio builds a portfolio that is exactly the named accounts.
func accountPortfolio(t *testing.T, p *Postgres, userID, name string, accounts ...string) string {
	t.Helper()
	ctx := context.Background()
	port, err := p.CreatePortfolio(ctx, userID, name)
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	filters := make([]db.PortfolioFilter, len(accounts))
	for i, a := range accounts {
		filters[i] = db.PortfolioFilter{FilterType: "account", FilterValue: a}
	}
	if err := p.SetPortfolioFilters(ctx, port.Id, filters); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	return port.Id
}

// matchTransfer records the link the transfermatch worker would write.
func matchTransfer(t *testing.T, p *Postgres, userID, from, to, instID string) {
	t.Helper()
	n, err := p.CreateTransferMatches(context.Background(), []db.TransferMatch{{
		UserID: userID, FromGroupID: from, ToGroupID: to,
		InstrumentID: instID, Method: db.TransferMatchPointer,
	}})
	if err != nil || n != 1 {
		t.Fatalf("create transfer match: wrote %d, err %v", n, err)
	}
}

// april is the window these tests value over: 10 April up to but not including
// 25 April, which brackets both sides of the transfer with days either side.
func april(day int) time.Time { return time.Date(2025, 4, day, 0, 0, 0, 0, time.UTC) }

// TestGetPortfolioValuation_MatchedPairHoldsValueFlat verifies that a transfer
// between two accounts of one portfolio does not move the portfolio's value on any
// day, including the five days it spends in transit. Admitting both clearing legs
// makes each group net to zero on its own date, so the running position never moves.
func TestGetPortfolioValuation_MatchedPairHoldsValueFlat(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-flat", "U", "u@flight-flat.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		got := values[april(day).Format("2006-01-02")]
		if got != 20000 {
			t.Errorf("value on April %d: want 20000, got %v", day, got)
		}
	}
}

// TestGetPortfolioValuation_MatchedPairIsInvisibleToHoldings verifies the other half
// of the same rule on the same fixture: a clearing leg valuation admits is still not
// a position, so mid-transit the departure account holds nothing and the arrival
// account does not hold it yet.
func TestGetPortfolioValuation_MatchedPairIsInvisibleToHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-hold", "U", "u@flight-hold.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port, timestamppb.New(april(17)))
	if err != nil {
		t.Fatalf("compute holdings: %v", err)
	}
	for _, h := range holdings {
		t.Errorf("expected no holdings in transit, got %s %s", h.GetSplitAdjustedQuantity(), h.GetInstrumentDescription())
	}
}

// TestGetPortfolioValuation_UnmatchedTransferDips verifies the behaviour the pairing
// exists to remove, so that the fixed case above is pinned against something. With no
// match the two clearing legs are excluded and the money is nowhere for five days.
func TestGetPortfolioValuation_UnmatchedTransferDips(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-dip", "U", "u@flight-dip.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	transferFixtureAt(t, p, userID, spec)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		want := 20000.0
		if day >= 15 && day < 20 {
			want = 0
		}
		got := values[april(day).Format("2006-01-02")]
		if got != want {
			t.Errorf("value on April %d: want %v, got %v", day, want, got)
		}
	}
}

// TestGetPortfolioValuation_MatchedPairOnlyDepartureIsMember verifies that a matched
// pair does not net for a portfolio holding only the account the value left. From
// that portfolio's point of view the money really did leave, and it does not come
// back.
func TestGetPortfolioValuation_MatchedPairOnlyDepartureIsMember(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-depart", "U", "u@flight-depart.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Departure", spec.fromAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		want := 20000.0
		if day >= 15 {
			want = 0
		}
		got := values[april(day).Format("2006-01-02")]
		if got != want {
			t.Errorf("value on April %d: want %v, got %v", day, want, got)
		}
	}
}

// TestGetPortfolioValuation_MatchedPairOnlyArrivalIsMember is the mirror: a portfolio
// holding only the receiving account sees nothing until the money arrives.
func TestGetPortfolioValuation_MatchedPairOnlyArrivalIsMember(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-arrive", "U", "u@flight-arrive.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Arrival", spec.toAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		want := 0.0
		if day >= 20 {
			want = 20000
		}
		got := values[april(day).Format("2006-01-02")]
		if got != want {
			t.Errorf("value on April %d: want %v, got %v", day, want, got)
		}
	}
}

// TestGetPortfolioValuation_CounterpartArrivesAfterWindowEnd verifies that value
// still in transit when the window closes is still value held. This is the test that
// fails if anyone date-bounds portfolio_in_flight_txs: the arrival falls outside the
// window entirely, and requiring it inside would reinstate the dip for exactly the
// days the pairing exists to cover.
func TestGetPortfolioValuation_CounterpartArrivesAfterWindowEnd(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-late", "U", "u@flight-late.com")
	spec := inFlightSpec(t, p, april(15), time.Date(2025, 5, 20, 0, 0, 0, 0, time.UTC))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		got := values[april(day).Format("2006-01-02")]
		if got != 20000 {
			t.Errorf("value on April %d: want 20000, got %v", day, got)
		}
	}
}

// TestGetPortfolioValuation_SecurityTransferValuedInTransit verifies that a journal
// moving shares rather than money is valued in transit at the share price, and does
// not report as unpriced.
func TestGetPortfolioValuation_SecurityTransferValuedInTransit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-sec", "U", "u@flight-sec.com")
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Corp", Domain: "FIDELITY"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	prices := make([]db.EODPrice, 0, 15)
	for day := 10; day < 25; day++ {
		prices = append(prices, db.EODPrice{
			ListingID: pricedListing(t, p, instID), PriceDate: april(day), Close: decf(150.0), DataProvider: "test",
		})
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	spec := transferSpec{
		instID: instID, desc: "AAPL Corp", qty: "-10",
		depart: april(15), arrive: april(20),
		fromAcct: "AG10000001", toAcct: "AW10000001",
	}
	openTxs := []*apiv1.Tx{{
		OrderDate: timestamppb.New(april(1)),
		TradeDate: timestamppb.New(april(1)), InstrumentDescription: "AAPL Corp",
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET,
		Quantity: "10", Account: spec.fromAcct,
	}}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", "",
		timestamppb.New(april(1).Add(-time.Hour)), timestamppb.New(april(1).Add(time.Hour)),
		openTxs, []string{instID}, nil, nil); err != nil {
		t.Fatalf("opening shares: %v", err)
	}
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, instID)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	points, err := p.GetPortfolioValuation(ctx, port, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		got := values[april(day).Format("2006-01-02")]
		if got != 1500 {
			t.Errorf("value on April %d: want 1500, got %v", day, got)
		}
	}
	for _, pt := range points {
		if len(pt.UnpricedInstruments) != 0 {
			t.Errorf("unpriced on %v: %v", pt.Date, pt.UnpricedInstruments)
		}
	}
}

// TestGetPortfolioValuation_MatchedPairIsScopedToItsOwnPortfolio verifies that the
// membership test names one portfolio on both sides. A pair matched between two
// accounts of one portfolio must not be admitted into another portfolio that holds
// neither.
func TestGetPortfolioValuation_MatchedPairIsScopedToItsOwnPortfolio(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-scope", "U", "u@flight-scope.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	openingCash(t, p, userID, spec.instID, "AX10000001", april(2), "500")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	other := accountPortfolio(t, p, userID, "Other", "AX10000001")

	points, err := p.GetPortfolioValuation(ctx, other, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		got := values[april(day).Format("2006-01-02")]
		if got != 500 {
			t.Errorf("value on April %d: want 500, got %v", day, got)
		}
	}
}

// TestGetUserValuation_MatchedPairHoldsValueFlat verifies the user-mode branch, which
// asks only whether a match names the group: every account of the user is in scope,
// so there is no membership left to test.
func TestGetUserValuation_MatchedPairHoldsValueFlat(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-user", "U", "u@flight-user.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)

	points, err := p.GetUserValuation(ctx, userID, april(10), april(25), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	values := dailyValues(points)
	for day := 10; day < 25; day++ {
		got := values[april(day).Format("2006-01-02")]
		if got != 20000 {
			t.Errorf("value on April %d: want 20000, got %v", day, got)
		}
	}
}

// TestGetUserValuation_MatchInAnotherCommodityIsNotAdmitted verifies that a match is
// keyed on the commodity as well as the group. A journal moving shares and money
// together leaves a residual in each, and matching the cash side says nothing about
// where the shares are: valuing them would assert they are coming back when only the
// money is accounted for.
func TestGetUserValuation_MatchInAnotherCommodityIsNotAdmitted(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|val-flight-commodity", "U", "u@flight-commodity.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	openingCash(t, p, userID, spec.instID, spec.fromAcct, april(1), "20000")

	secID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "AAPL Corp", Domain: "FIDELITY"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, secID), PriceDate: april(15), Close: decf(150.0), DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}
	openTxs := []*apiv1.Tx{{
		OrderDate: timestamppb.New(april(2)),
		TradeDate: timestamppb.New(april(2)), InstrumentDescription: "AAPL Corp",
		BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET,
		Quantity: "10", Account: spec.fromAcct,
	}}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", "",
		timestamppb.New(april(2).Add(-time.Hour)), timestamppb.New(april(2).Add(time.Hour)),
		openTxs, []string{secID}, nil, nil); err != nil {
		t.Fatalf("opening shares: %v", err)
	}

	from, to := transferFixtureAt(t, p, userID, spec)

	// The departure group also moves the shares out, leaving a second residual in a
	// commodity the match will not name. Written directly because the fixture builds
	// one commodity, and as a balanced pair so the group invariant still holds.
	for _, leg := range []struct {
		qty         string
		accountType string
	}{{"-10", "USER"}, {"10", "TRANSFER_CLEARING"}} {
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
				instrument_id, broker_tx_type, resolved_tx_type, quantity, account_type,
				group_id, weight, weight_commodity, share_count_basis, split_adjusted_quantity)
			VALUES ($1::uuid, 'FIDELITY', $2, $3::timestamptz, $3::timestamptz, 'AAPL Corp', $4::uuid,
				ARRAY['TRANSFER'], 'TRANSFER', $5::numeric, $6,
				$7::uuid, $5::numeric, 'inst:'||$4, $3::timestamptz::date, $5::numeric)
		`, userID, spec.fromAcct, april(15), secID, leg.qty, leg.accountType, from); err != nil {
			t.Fatalf("insert %s security leg: %v", leg.accountType, err)
		}
	}
	matchTransfer(t, p, userID, from, to, spec.instID)

	points, err := p.GetUserValuation(ctx, userID, april(15), april(16), "USD")
	if err != nil {
		t.Fatalf("get valuation: %v", err)
	}
	// The cash is matched and holds flat at 20,000. The shares have left and their
	// clearing leg is unmatched, so their 10 x 150 must not be added back.
	if got := dailyValues(points)[april(15).Format("2006-01-02")]; got != 20000 {
		t.Errorf("value on April 15: want 20000, got %v", got)
	}
}
