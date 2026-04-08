package postgres

import (
	"context"
	"math"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// approxEq compares two floats with relative tolerance suitable for the
// exp(sum(ln())) split factor implementation.
func approxEq(a, b float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b)/math.Max(math.Abs(a), math.Abs(b)) < 1e-9
}

func TestUpsertStockSplits_InsertAndOverwrite(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2014, 6, 9), SplitFrom: "1", SplitTo: "7", DataProvider: "massive"},
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "massive"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := p.ListStockSplits(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 splits, got %d", len(got))
	}
	if !got[0].ExDate.Equal(d(2014, 6, 9)) || got[0].SplitTo != "7" {
		t.Errorf("first split: %+v", got[0])
	}
	if got[1].DataProvider != "massive" {
		t.Errorf("provider: %q", got[1].DataProvider)
	}

	// Overwrite with a different provider; should update in place.
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2014, 6, 9), SplitFrom: "1", SplitTo: "7", DataProvider: "eodhd"},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err = p.ListStockSplits(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].DataProvider != "eodhd" {
		t.Fatalf("expected first row provider=eodhd, got %+v", got)
	}
}

func TestDeleteStockSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "massive"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := p.DeleteStockSplit(ctx, instID, d(2020, 8, 31)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := p.ListStockSplits(ctx, instID)
	if len(got) != 0 {
		t.Fatalf("expected 0 splits after delete, got %d", len(got))
	}
}

func TestUpsertCashDividends_RoundTrip(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	pay := d(2024, 2, 15)
	rec := d(2024, 2, 12)
	decl := d(2024, 2, 1)
	if err := p.UpsertCashDividends(ctx, []db.CashDividend{
		{
			InstrumentID:    instID,
			ExDate:          d(2024, 2, 9),
			PayDate:         &pay,
			RecordDate:      &rec,
			DeclarationDate: &decl,
			Amount:          "0.24",
			Currency:        "USD",
			Frequency:       "quarterly",
			DataProvider:    "massive",
		},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got))
	}
	d0 := got[0]
	if d0.Amount != "0.24" || d0.Currency != "USD" || d0.Frequency != "quarterly" {
		t.Errorf("dividend: %+v", d0)
	}
	if d0.PayDate == nil || !d0.PayDate.Equal(pay) {
		t.Errorf("pay_date: %+v", d0.PayDate)
	}
	if d0.RecordDate == nil || !d0.RecordDate.Equal(rec) {
		t.Errorf("record_date: %+v", d0.RecordDate)
	}
	if d0.DeclarationDate == nil || !d0.DeclarationDate.Equal(decl) {
		t.Errorf("declaration_date: %+v", d0.DeclarationDate)
	}
}

func TestUpsertCorporateEventCoverage_MergeAdjacent(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert three intervals: [Jan1, Jan10], [Jan11, Jan20] (adjacent),
	// [Feb1, Feb10] (separate).
	for _, iv := range []struct{ from, to time.Time }{
		{d(2024, 1, 1), d(2024, 1, 10)},
		{d(2024, 1, 11), d(2024, 1, 20)},
		{d(2024, 2, 1), d(2024, 2, 10)},
	} {
		if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", iv.from, iv.to); err != nil {
			t.Fatalf("upsert coverage %v: %v", iv, err)
		}
	}

	got, err := p.ListCorporateEventCoverage(ctx, []string{instID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 merged intervals, got %d: %+v", len(got), got)
	}
	if !got[0].CoveredFrom.Equal(d(2024, 1, 1)) || !got[0].CoveredTo.Equal(d(2024, 1, 20)) {
		t.Errorf("first merged interval: %+v", got[0])
	}
	if !got[1].CoveredFrom.Equal(d(2024, 2, 1)) || !got[1].CoveredTo.Equal(d(2024, 2, 10)) {
		t.Errorf("second interval: %+v", got[1])
	}
}

func TestUpsertCorporateEventCoverage_MergeOverlapping(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert two overlapping intervals: [Jan1, Jan15] and [Jan10, Jan20].
	for _, iv := range []struct{ from, to time.Time }{
		{d(2024, 1, 1), d(2024, 1, 15)},
		{d(2024, 1, 10), d(2024, 1, 20)},
	} {
		if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", iv.from, iv.to); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, _ := p.ListCorporateEventCoverage(ctx, []string{instID})
	if len(got) != 1 {
		t.Fatalf("expected 1 merged interval, got %d: %+v", len(got), got)
	}
	if !got[0].CoveredFrom.Equal(d(2024, 1, 1)) || !got[0].CoveredTo.Equal(d(2024, 1, 20)) {
		t.Errorf("merged interval: %+v", got[0])
	}
}

func TestUpsertCorporateEventCoverage_PerPlugin(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Different plugins should not be merged together.
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", d(2024, 1, 1), d(2024, 1, 31)); err != nil {
		t.Fatalf("upsert massive: %v", err)
	}
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "eodhd", d(2024, 1, 15), d(2024, 2, 15)); err != nil {
		t.Fatalf("upsert eodhd: %v", err)
	}

	got, _ := p.ListCorporateEventCoverage(ctx, []string{instID})
	if len(got) != 2 {
		t.Fatalf("expected 2 rows (one per plugin), got %d", len(got))
	}
	plugins := map[string]bool{got[0].PluginID: true, got[1].PluginID: true}
	if !plugins["massive"] || !plugins["eodhd"] {
		t.Errorf("expected both plugins, got %+v", got)
	}
}

func TestCorporateEventFetchBlocks(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.CreateCorporateEventFetchBlock(ctx, instID, "massive", "404 not found"); err != nil {
		t.Fatalf("create block: %v", err)
	}
	blocks, err := p.ListCorporateEventFetchBlocks(ctx)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].PluginID != "massive" {
		t.Fatalf("expected one block for massive, got %+v", blocks)
	}

	bymap, err := p.BlockedCorporateEventPluginsForInstruments(ctx, []string{instID})
	if err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if !bymap[instID]["massive"] {
		t.Errorf("expected massive blocked for %s", instID)
	}

	if err := p.DeleteCorporateEventFetchBlock(ctx, instID, "massive"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	blocks, _ = p.ListCorporateEventFetchBlocks(ctx)
	if len(blocks) != 0 {
		t.Fatalf("expected zero blocks after delete, got %d", len(blocks))
	}
}

// TestRecomputeSplitAdjustments_Prices verifies that a sequence of splits
// (forward + reverse) is applied correctly to historical price rows whose
// fetched_at predates the split ex_date.
func TestRecomputeSplitAdjustments_Prices(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert prices fetched in 2010 (before any splits).
	insertPriceFull(t, p, instID, d(2005, 1, 3), 80, 82, 79, 81, 1_000_000, "test")
	// Backdate fetched_at to 2010-01-01 so future-dated splits apply.
	if _, err := p.q.ExecContext(ctx, `
		UPDATE eod_prices SET fetched_at = $1 WHERE instrument_id = $2::uuid
	`, d(2010, 1, 1), instID); err != nil {
		t.Fatalf("backdate fetched_at: %v", err)
	}

	// Two forward splits and one reverse split.
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2014, 6, 9), SplitFrom: "1", SplitTo: "7", DataProvider: "test"},
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
		{InstrumentID: instID, ExDate: d(2022, 1, 3), SplitFrom: "2", SplitTo: "1", DataProvider: "test"}, // reverse 1:2
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	// Cumulative factor = 7 * 4 * 0.5 = 14.
	rows, _, _, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 30, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// ListPrices does not return split-adjusted columns; query directly.
	var saOpen, saHigh, saLow, saClose float64
	var saVolume int64
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_open, split_adjusted_high, split_adjusted_low,
			split_adjusted_close, split_adjusted_volume
		FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&saOpen, &saHigh, &saLow, &saClose, &saVolume); err != nil {
		t.Fatalf("read adjusted: %v", err)
	}
	const factor = 14.0
	if !approxEq(saOpen, 80/factor) {
		t.Errorf("split_adjusted_open: got %v want %v", saOpen, 80/factor)
	}
	if !approxEq(saHigh, 82/factor) {
		t.Errorf("split_adjusted_high: got %v want %v", saHigh, 82/factor)
	}
	if !approxEq(saLow, 79/factor) {
		t.Errorf("split_adjusted_low: got %v want %v", saLow, 79/factor)
	}
	if !approxEq(saClose, 81/factor) {
		t.Errorf("split_adjusted_close: got %v want %v", saClose, 81/factor)
	}
	// Volume scales the opposite way (more shares trade in adjusted-share terms).
	if saVolume != int64(math.Round(1_000_000*factor)) {
		t.Errorf("split_adjusted_volume: got %d want %d", saVolume, int64(math.Round(1_000_000*factor)))
	}

	// Idempotency: second recompute should leave state unchanged.
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute again: %v", err)
	}
	var saClose2 float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_close FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&saClose2); err != nil {
		t.Fatalf("read adjusted (2): %v", err)
	}
	if saClose != saClose2 {
		t.Errorf("idempotency: %v vs %v", saClose, saClose2)
	}
}

// TestRecomputeSplitAdjustments_Txs verifies that a tx whose timestamp predates
// a split has its quantity multiplied and unit_price divided by the cumulative
// factor.
func TestRecomputeSplitAdjustments_Txs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "AAPL")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{
			Type:                  apiv1.TxType_BUYSTOCK,
			Timestamp:             ts(2010, 6, 1),
			Quantity:              100,
			UnitPrice:             280.0,
			InstrumentDescription: "AAPL",
		},
	})

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2014, 6, 9), SplitFrom: "1", SplitTo: "7", DataProvider: "test"},
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	const factor = 28.0
	var qty, saQty float64
	var unitPrice, saUnitPrice float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT quantity, split_adjusted_quantity, unit_price, split_adjusted_unit_price
		FROM txs WHERE instrument_id = $1::uuid
	`, instID).Scan(&qty, &saQty, &unitPrice, &saUnitPrice); err != nil {
		t.Fatalf("read tx: %v", err)
	}
	if qty != 100 {
		t.Errorf("raw quantity unchanged: got %v", qty)
	}
	if !approxEq(saQty, 100*factor) {
		t.Errorf("split_adjusted_quantity: got %v want %v", saQty, 100*factor)
	}
	if unitPrice != 280 {
		t.Errorf("raw unit_price unchanged: got %v", unitPrice)
	}
	if !approxEq(saUnitPrice, 280/factor) {
		t.Errorf("split_adjusted_unit_price: got %v want %v", saUnitPrice, 280/factor)
	}

	// Cost-basis invariant: qty * unit_price == saQty * saUnitPrice.
	if !approxEq(qty*unitPrice, saQty*saUnitPrice) {
		t.Errorf("cost-basis invariant violated: %v vs %v", qty*unitPrice, saQty*saUnitPrice)
	}
}

// TestRecomputeSplitAdjustments_FutureSplitNotApplied verifies that a split
// stored in stock_splits with ex_date in the future does NOT affect the
// recompute. Corporate event plugins return announced splits weeks before
// they are effective, and the lookahead window pulls them into the database
// early; without the future-date guard in split_factor_at, every prior
// price/tx for the instrument would be scaled immediately on fetch, even
// though the user still owns pre-split shares trading at pre-split prices.
func TestRecomputeSplitAdjustments_FutureSplitNotApplied(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	insertPriceFull(t, p, instID, d(2024, 1, 15), 180, 182, 178, 181, 1000, "test")
	// Backdate fetched_at to 2024-01-15 so the recompute considers the
	// past split (whose ex_date is later in 2024) as "after fetch" and
	// applies it. Without backdating, the price's fetched_at would be
	// today and the 2024 past split would be excluded as "before fetch".
	if _, err := p.q.ExecContext(ctx, `
		UPDATE eod_prices SET fetched_at = $1 WHERE instrument_id = $2::uuid
	`, d(2024, 1, 15), instID); err != nil {
		t.Fatalf("backdate fetched_at: %v", err)
	}

	// Insert a split with ex_date in the future. The key assertion is
	// that this row sits in stock_splits but does NOT scale the price,
	// because split_factor_at filters splits with ex_date > current_date.
	future := time.Now().UTC().Truncate(24 * time.Hour).AddDate(1, 0, 0)
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: future, SplitFrom: "1", SplitTo: "2", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert split: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	var saClose, rawClose float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT close, split_adjusted_close FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&rawClose, &saClose); err != nil {
		t.Fatalf("read: %v", err)
	}
	if rawClose != 181 {
		t.Errorf("raw close: got %v want 181", rawClose)
	}
	if saClose != 181 {
		t.Errorf("split_adjusted_close should equal raw (future split is inert), got %v", saClose)
	}

	// Sanity check: a second split with ex_date in the past (and after
	// fetched_at) IS applied. This proves the recompute is functional and
	// the previous result is specifically because of the future guard,
	// not because the recompute is silently broken.
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2024, 6, 1), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert past split: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute (2): %v", err)
	}
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_close FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&saClose); err != nil {
		t.Fatalf("read (2): %v", err)
	}
	// Past split with factor=4 applies; future split is still inert.
	if !approxEq(saClose, 181.0/4.0) {
		t.Errorf("split_adjusted_close after past split: got %v want %v", saClose, 181.0/4.0)
	}
}

// TestRecomputeSplitAdjustments_NoSplits verifies that with no splits the
// adjusted columns equal the raw values (factor = 1).
func TestRecomputeSplitAdjustments_NoSplits(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "GOOG")

	insertPriceFull(t, p, instID, d(2024, 1, 15), 100, 105, 99, 102, 1000, "test")

	// No splits exist; recompute is a no-op for this instrument because the
	// instFilter excludes it. The trigger has already seeded adjusted = raw.
	if err := p.RecomputeSplitAdjustments(ctx, ""); err != nil {
		t.Fatalf("recompute all: %v", err)
	}

	var saClose float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_close FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&saClose); err != nil {
		t.Fatalf("read: %v", err)
	}
	if saClose != 102 {
		t.Errorf("expected split_adjusted_close = close = 102, got %v", saClose)
	}
}

// TestListStockSplitsForExport_BestIdentifier verifies that the export query
// joins each split with the highest-priority identifier for the instrument.
// MIC_TICKER beats ISIN beats BROKER_DESCRIPTION.
func TestListStockSplitsForExport_BestIdentifier(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create an instrument with three identifiers, MIC_TICKER should win.
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "TEST", Value: "Apple Inc.", Canonical: false},
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rows, err := p.ListStockSplitsForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].IdentifierType != "MIC_TICKER" || rows[0].IdentifierValue != "AAPL" {
		t.Errorf("expected MIC_TICKER/AAPL, got %s/%s", rows[0].IdentifierType, rows[0].IdentifierValue)
	}
	if rows[0].IdentifierDomain != "XNAS" {
		t.Errorf("expected domain XNAS, got %q", rows[0].IdentifierDomain)
	}
	if rows[0].AssetClass != "STOCK" {
		t.Errorf("expected STOCK, got %q", rows[0].AssetClass)
	}
	if rows[0].SplitFrom != "1" || rows[0].SplitTo != "4" {
		t.Errorf("split: %+v", rows[0])
	}
}

// TestListCashDividendsForExport_RoundTrip verifies that all optional fields
// flow through the export query.
func TestListCashDividendsForExport_RoundTrip(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "", "", "", []db.IdentifierInput{
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	pay := d(2024, 2, 15)
	rec := d(2024, 2, 12)
	decl := d(2024, 2, 1)
	if err := p.UpsertCashDividends(ctx, []db.CashDividend{
		{
			InstrumentID:    instID,
			ExDate:          d(2024, 2, 9),
			PayDate:         &pay,
			RecordDate:      &rec,
			DeclarationDate: &decl,
			Amount:          "0.24",
			Currency:        "USD",
			Frequency:       "quarterly",
			DataProvider:    "test",
		},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rows, err := p.ListCashDividendsForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.IdentifierType != "MIC_TICKER" || r.IdentifierValue != "AAPL" {
		t.Errorf("identifier: %+v", r)
	}
	if r.Amount != "0.24" || r.Currency != "USD" || r.Frequency != "quarterly" {
		t.Errorf("payload: %+v", r)
	}
	if r.PayDate == nil || !r.PayDate.Equal(pay) {
		t.Errorf("pay date: %+v", r.PayDate)
	}
	if r.RecordDate == nil || !r.RecordDate.Equal(rec) {
		t.Errorf("record date: %+v", r.RecordDate)
	}
	if r.DeclarationDate == nil || !r.DeclarationDate.Equal(decl) {
		t.Errorf("declaration date: %+v", r.DeclarationDate)
	}
}

// TestListStockSplitsForExport_ExcludesInstrumentsWithoutIdentifiers verifies
// that an instrument with no identifiers does not appear in export output.
func TestListStockSplitsForExport_ExcludesInstrumentsWithoutIdentifiers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Insert a bare instrument (no identifiers) directly. EnsureInstrument
	// requires at least one identifier, so we side-step it.
	var instID string
	if err := p.q.QueryRowContext(ctx, `
		INSERT INTO instruments (asset_class) VALUES ('STOCK') RETURNING id::text
	`).Scan(&instID); err != nil {
		t.Fatalf("insert instrument: %v", err)
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 1, 1), SplitFrom: "1", SplitTo: "2", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rows, err := p.ListStockSplitsForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows (no identifiers), got %d", len(rows))
	}
}

// TestSplitAdjustment_TriggerSeeds verifies that the BEFORE INSERT trigger
// seeds split_adjusted_* to the raw counterparts on a fresh insert via the
// existing UpsertPrices path, with no explicit recompute call.
func TestSplitAdjustment_TriggerSeeds(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "MSFT")

	open, high, low := 380.0, 385.0, 378.0
	vol := int64(123456)
	if err := p.UpsertPrices(ctx, []db.EODPrice{{
		InstrumentID: instID,
		PriceDate:    d(2024, 3, 1),
		Open:         &open,
		High:         &high,
		Low:          &low,
		Close:        382.5,
		Volume:       &vol,
		DataProvider: "test",
	}}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	var saOpen, saHigh, saLow, saClose float64
	var saVolume int64
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_open, split_adjusted_high, split_adjusted_low,
			split_adjusted_close, split_adjusted_volume
		FROM eod_prices WHERE instrument_id = $1::uuid
	`, instID).Scan(&saOpen, &saHigh, &saLow, &saClose, &saVolume); err != nil {
		t.Fatalf("read: %v", err)
	}
	if saOpen != 380 || saHigh != 385 || saLow != 378 || saClose != 382.5 || saVolume != 123456 {
		t.Errorf("trigger did not seed adjusted=raw: got open=%v high=%v low=%v close=%v vol=%d",
			saOpen, saHigh, saLow, saClose, saVolume)
	}
}

