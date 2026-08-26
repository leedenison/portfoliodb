package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/derivative"
	"google.golang.org/protobuf/proto"
	"math"
	"testing"
	"time"
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
	upsertDividends(t, p, db.CashDividend{
		InstrumentID:    instID,
		ExDate:          d(2024, 2, 9),
		PayDate:         &pay,
		RecordDate:      &rec,
		DeclarationDate: &decl,
		Amount:          "0.24",
		Currency:        "USD",
		Frequency:       "quarterly",
		DataProvider:    "massive",
	})

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
	if d0.Type != "CD" {
		t.Errorf("type: got %q, want %q", d0.Type, "CD")
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

func TestUpsertCashDividends_TypeDefaultsToCD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert without setting Type (empty string) — should default to "CD".
	upsertDividends(t, p, db.CashDividend{
		InstrumentID: instID,
		ExDate:       d(2024, 3, 1),
		Amount:       "0.50",
		Currency:     "USD",
		DataProvider: "test",
	})

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got))
	}
	if got[0].Type != "CD" {
		t.Errorf("type: got %q, want %q", got[0].Type, "CD")
	}
}

func TestUpsertStockSplits_PreservesKnowledgeTime(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	split := db.StockSplit{
		InstrumentID: instID, ExDate: d(2020, 8, 31),
		SplitFrom: "1", SplitTo: "4", DataProvider: "massive",
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	past := backdate(t, p,
		`UPDATE stock_splits SET first_known_at = $1 WHERE instrument_id = $2`, instID)

	// The provider revises the ratio. When we first learned of the split does
	// not change just because the ratio did -- it is carried across corporate
	// event export and import.
	split.SplitTo = "5"
	split.DataProvider = "eodhd"
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	got, err := p.ListStockSplits(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 split, got %d", len(got))
	}
	if got[0].SplitTo != "5" {
		t.Errorf("ratio not revised: %q", got[0].SplitTo)
	}
	if !got[0].FirstKnownAt.Equal(past) {
		t.Errorf("knowledge time moved on revision: want %s, got %s", past, got[0].FirstKnownAt)
	}
}

// A supplied knowledge time is honoured on insert, and on conflict it only
// moves backwards. This is what makes an export/import round trip lossless:
// re-importing a split we learned of years ago must not restamp it with the
// import time, which would make every option look identified-before-we-knew.
func TestUpsertStockSplits_KnowledgeTimeOnlyMovesBackwards(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	original := time.Date(2015, time.March, 4, 9, 30, 0, 0, time.UTC)
	split := db.StockSplit{
		InstrumentID: instID, ExDate: d(2020, 8, 31),
		SplitFrom: "1", SplitTo: "4", DataProvider: "import",
		FirstKnownAt: original,
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := p.ListStockSplits(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || !got[0].FirstKnownAt.Equal(original) {
		t.Fatalf("supplied knowledge time not honoured on insert: %+v", got)
	}

	// A later stamp loses.
	split.FirstKnownAt = time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("re-upsert later: %v", err)
	}
	got, _ = p.ListStockSplits(ctx, instID)
	if !got[0].FirstKnownAt.Equal(original) {
		t.Errorf("later stamp won: want %s, got %s", original, got[0].FirstKnownAt)
	}

	// An earlier stamp wins.
	earlier := time.Date(2014, time.June, 9, 0, 0, 0, 0, time.UTC)
	split.FirstKnownAt = earlier
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("re-upsert earlier: %v", err)
	}
	got, _ = p.ListStockSplits(ctx, instID)
	if !got[0].FirstKnownAt.Equal(earlier) {
		t.Errorf("earlier stamp lost: want %s, got %s", earlier, got[0].FirstKnownAt)
	}

	// A zero stamp leaves the stored value alone rather than restamping now().
	split.FirstKnownAt = time.Time{}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{split}); err != nil {
		t.Fatalf("re-upsert zero: %v", err)
	}
	got, _ = p.ListStockSplits(ctx, instID)
	if !got[0].FirstKnownAt.Equal(earlier) {
		t.Errorf("zero stamp overwrote stored value: want %s, got %s", earlier, got[0].FirstKnownAt)
	}
}

func TestUpsertCashDividends_KnowledgeTimeOnlyMovesBackwards(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	original := time.Date(2015, time.March, 4, 9, 30, 0, 0, time.UTC)
	div := db.CashDividend{
		InstrumentID: instID, ExDate: d(2024, 2, 9),
		Amount: "0.24", Currency: "USD", DataProvider: "import",
		FirstKnownAt: original,
	}
	upsertDividends(t, p, div)
	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || !got[0].FirstKnownAt.Equal(original) {
		t.Fatalf("supplied knowledge time not honoured on insert: %+v", got)
	}

	div.FirstKnownAt = time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)
	upsertDividends(t, p, div)
	got, _ = p.ListCashDividends(ctx, instID)
	if !got[0].FirstKnownAt.Equal(original) {
		t.Errorf("later stamp won: want %s, got %s", original, got[0].FirstKnownAt)
	}
}

func TestUpsertCorporateEventCoverage_MergeAdjacent(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert three intervals: [Jan1, Jan11) and [Jan11, Jan21) abut, so they
	// merge with no adjacency arithmetic; [Feb1, Feb11) stays separate.
	for _, iv := range []struct{ from, before time.Time }{
		{d(2024, 1, 1), d(2024, 1, 11)},
		{d(2024, 1, 11), d(2024, 1, 21)},
		{d(2024, 2, 1), d(2024, 2, 11)},
	} {
		if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", iv.from, iv.before, nil); err != nil {
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
	if !got[0].CoveredFrom.Equal(d(2024, 1, 1)) || !got[0].CoveredBefore.Equal(d(2024, 1, 21)) {
		t.Errorf("first merged interval: %+v", got[0])
	}
	if !got[1].CoveredFrom.Equal(d(2024, 2, 1)) || !got[1].CoveredBefore.Equal(d(2024, 2, 11)) {
		t.Errorf("second interval: %+v", got[1])
	}
}

func TestUpsertCorporateEventCoverage_MergeOverlapping(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert two overlapping intervals: [Jan1, Jan16) and [Jan10, Jan21).
	for _, iv := range []struct{ from, before time.Time }{
		{d(2024, 1, 1), d(2024, 1, 16)},
		{d(2024, 1, 10), d(2024, 1, 21)},
	} {
		if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", iv.from, iv.before, nil); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, _ := p.ListCorporateEventCoverage(ctx, []string{instID})
	if len(got) != 1 {
		t.Fatalf("expected 1 merged interval, got %d: %+v", len(got), got)
	}
	if !got[0].CoveredFrom.Equal(d(2024, 1, 1)) || !got[0].CoveredBefore.Equal(d(2024, 1, 21)) {
		t.Errorf("merged interval: %+v", got[0])
	}
}

// A one-day gap must not close itself: the fetcher would otherwise believe it
// had asked about a date it never asked about.
func TestUpsertCorporateEventCoverage_LeavesGapUnmerged(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// [Jan1, Jan11) covers through Jan 10; [Jan12, Jan20) starts a day later,
	// leaving Jan 11 uncovered.
	for _, iv := range []struct{ from, before time.Time }{
		{d(2024, 1, 1), d(2024, 1, 11)},
		{d(2024, 1, 12), d(2024, 1, 20)},
	} {
		if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", iv.from, iv.before, nil); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, _ := p.ListCorporateEventCoverage(ctx, []string{instID})
	if len(got) != 2 {
		t.Fatalf("expected the gap to keep the intervals apart, got %d: %+v", len(got), got)
	}
}

func TestUpsertCorporateEventCoverage_RejectsEmptyInterval(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	day := d(2024, 1, 1)
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", day, day, nil); err == nil {
		t.Fatal("expected an empty interval to be rejected")
	}
}

// Merging spans must not restamp the union as freshly confirmed. The fetcher
// extends coverage by a day at a time at the trailing edge, so a span covering
// years would otherwise claim to have been confirmed today on every cycle.
func TestUpsertCorporateEventCoverage_MergeKeepsOldestFetchTime(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	old := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", d(2015, 1, 1), d(2020, 3, 1), &old); err != nil {
		t.Fatalf("upsert historical span: %v", err)
	}
	// A fresh trailing-edge fetch of the single day that abuts the historical span.
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", d(2020, 3, 1), d(2020, 3, 2), nil); err != nil {
		t.Fatalf("upsert trailing edge: %v", err)
	}

	got, err := p.ListCorporateEventCoverage(ctx, []string{instID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 merged interval, got %d: %+v", len(got), got)
	}
	if !got[0].CoveredFrom.Equal(d(2015, 1, 1)) || !got[0].CoveredBefore.Equal(d(2020, 3, 2)) {
		t.Errorf("merged interval: %+v", got[0])
	}
	if !got[0].LastFetchedAt.Equal(old) {
		t.Errorf("merge restamped the union: want %s, got %s", old, got[0].LastFetchedAt)
	}
}

func TestUpsertCorporateEventCoverage_PerPlugin(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Different plugins should not be merged together.
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", d(2024, 1, 1), d(2024, 2, 1), nil); err != nil {
		t.Fatalf("upsert massive: %v", err)
	}
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "eodhd", d(2024, 1, 15), d(2024, 2, 16), nil); err != nil {
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
// last_fetched_at predates the split ex_date.
func TestRecomputeSplitAdjustments_Prices(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Insert prices fetched in 2010 (before any splits).
	insertPriceFull(t, p, instID, d(2005, 1, 3), 80, 82, 79, 81, 1_000_000, "test")
	// Backdate last_fetched_at to 2010-01-01 so future-dated splits apply.
	if _, err := p.q.ExecContext(ctx, `
		UPDATE eod_prices SET last_fetched_at = $1 WHERE listing_id = $2::uuid
	`, d(2010, 1, 1), pricedListing(t, p, instID)); err != nil {
		t.Fatalf("backdate last_fetched_at: %v", err)
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
		FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&saOpen, &saHigh, &saLow, &saClose, &saVolume); err != nil {
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
		SELECT split_adjusted_close FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&saClose2); err != nil {
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
			BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
			ResolvedTxType:        typev1.TxType_TRADE_ASSET,
			OrderDate:             ts(2010, 6, 1),
			TradeDate:             ts(2010, 6, 1),
			Quantity:              "100",
			UnitPrice:             proto.String("280"),
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
	// Backdate last_fetched_at to 2024-01-15 so the recompute considers the
	// past split (whose ex_date is later in 2024) as "after fetch" and
	// applies it. Without backdating, the price's last_fetched_at would be
	// today and the 2024 past split would be excluded as "before fetch".
	if _, err := p.q.ExecContext(ctx, `
		UPDATE eod_prices SET last_fetched_at = $1 WHERE listing_id = $2::uuid
	`, d(2024, 1, 15), pricedListing(t, p, instID)); err != nil {
		t.Fatalf("backdate last_fetched_at: %v", err)
	}

	// Insert a split with ex_date in the future. The key assertion is
	// that this row sits in stock_splits but does NOT scale the price,
	// because split_factor_at filters splits with ex_date > current_date.
	future := time.Now().UTC().Truncate(24*time.Hour).AddDate(1, 0, 0)
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
		SELECT close, split_adjusted_close FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&rawClose, &saClose); err != nil {
		t.Fatalf("read: %v", err)
	}
	if rawClose != 181 {
		t.Errorf("raw close: got %v want 181", rawClose)
	}
	if saClose != 181 {
		t.Errorf("split_adjusted_close should equal raw (future split is inert), got %v", saClose)
	}

	// Sanity check: a second split with ex_date in the past (and after
	// last_fetched_at) IS applied. This proves the recompute is functional and
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
		SELECT split_adjusted_close FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&saClose); err != nil {
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
		SELECT split_adjusted_close FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&saClose); err != nil {
		t.Fatalf("read: %v", err)
	}
	if saClose != 102 {
		t.Errorf("expected split_adjusted_close = close = 102, got %v", saClose)
	}
}

// TestRecomputeSplitAdjustments_ForwardSplitIsExact asserts on the stored text of
// the adjusted columns rather than on a float64, because the point of the num/den
// factor is that a forward split leaves no rounding at all. A 7:1 then 4:1 chain
// on 100 shares at 280 is 2800 shares at 10 exactly -- the old exp(sum(ln(...)))
// factor reached 27.999999999999996 and both adjusted values inherited it.
func TestRecomputeSplitAdjustments_ForwardSplitIsExact(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "AAPL")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{{
		BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType:        typev1.TxType_TRADE_ASSET,
		OrderDate:             ts(2010, 6, 1),
		TradeDate:             ts(2010, 6, 1),
		Quantity:              "100",
		UnitPrice:             proto.String("280"),
		InstrumentDescription: "AAPL",
	}})
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2014, 6, 9), SplitFrom: "1", SplitTo: "7", DataProvider: "test"},
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	// Read as text so the assertion is on the stored decimal, not on a value the
	// driver has already rounded into a float64.
	var saQty, saUnitPrice string
	if err := p.q.QueryRowContext(ctx, `
		SELECT trim_scale(split_adjusted_quantity)::text,
		       trim_scale(split_adjusted_unit_price)::text
		FROM txs WHERE instrument_id = $1::uuid
	`, instID).Scan(&saQty, &saUnitPrice); err != nil {
		t.Fatalf("read tx: %v", err)
	}
	if saQty != "2800" {
		t.Errorf("split_adjusted_quantity: got %q want %q", saQty, "2800")
	}
	if saUnitPrice != "10" {
		t.Errorf("split_adjusted_unit_price: got %q want %q", saUnitPrice, "10")
	}
}

// TestRecomputeSplitAdjustments_ReverseSplitRoundsToDeclaredScale covers the case
// the declared scale exists for. A 1:3 reverse split on 100 shares is 33.333...,
// which has no finite decimal form, so the column rounds it at 12 places. The
// price moves the other way and 280 * 3 is exact, so only one side of the pair
// rounds -- which is why the cost-basis invariant is checked on the raw columns.
func TestRecomputeSplitAdjustments_ReverseSplitRoundsToDeclaredScale(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "RVRS")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{{
		BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType:        typev1.TxType_TRADE_ASSET,
		OrderDate:             ts(2020, 1, 2),
		TradeDate:             ts(2020, 1, 2),
		Quantity:              "100",
		UnitPrice:             proto.String("280"),
		InstrumentDescription: "RVRS",
	}})
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2021, 3, 1), SplitFrom: "3", SplitTo: "1", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	var saQty, saUnitPrice string
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_quantity::text, trim_scale(split_adjusted_unit_price)::text
		FROM txs WHERE instrument_id = $1::uuid
	`, instID).Scan(&saQty, &saUnitPrice); err != nil {
		t.Fatalf("read tx: %v", err)
	}
	if saQty != "33.333333333333" {
		t.Errorf("split_adjusted_quantity: got %q want %q (12dp)", saQty, "33.333333333333")
	}
	if saUnitPrice != "840" {
		t.Errorf("split_adjusted_unit_price: got %q want %q", saUnitPrice, "840")
	}
}

// TestListStockSplitsForExport_BestIdentifier verifies that the export query
// joins each split with the identifier the security join ranks highest.
//
// A split is an action on the shares rather than on one of the currency lines
// they are quoted in, so the security is what a split names, and the ISIN is what
// names a security: ISIN beats MIC_TICKER beats BROKER_DESCRIPTION. A ticker
// would name one line of it and say nothing about the others, which is why the
// two grains no longer share one priority order.
func TestListStockSplitsForExport_BestIdentifier(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create an instrument with three identifiers, MIC_TICKER should win.
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "Apple Inc.", Domain: "TEST"},
			Canonical: false,
		},
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	knownAt := time.Date(2015, time.March, 4, 9, 30, 0, 0, time.UTC)
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test",
			FirstKnownAt: knownAt},
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
	if rows[0].Ref.Type != "ISIN" || rows[0].Ref.Value != "US0378331005" {
		t.Errorf("expected ISIN/US0378331005, got %s/%s", rows[0].Ref.Type, rows[0].Ref.Value)
	}
	// An ISIN is globally unique and carries no domain.
	if rows[0].Ref.Domain != "" {
		t.Errorf("expected no domain, got %q", rows[0].Ref.Domain)
	}
	if rows[0].AssetClass != "STOCK" {
		t.Errorf("expected STOCK, got %q", rows[0].AssetClass)
	}
	if rows[0].SplitFrom != "1" || rows[0].SplitTo != "4" {
		t.Errorf("split: %+v", rows[0])
	}
	// Knowledge time must reach the wire, or a round trip restamps the split.
	if !rows[0].FirstKnownAt.Equal(knownAt) {
		t.Errorf("first known at: want %s, got %s", knownAt, rows[0].FirstKnownAt)
	}
}

// TestListCashDividendsForExport_RoundTrip verifies that all optional fields
// flow through the export query.
func TestListCashDividendsForExport_RoundTrip(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	pay := d(2024, 2, 15)
	rec := d(2024, 2, 12)
	decl := d(2024, 2, 1)
	upsertDividends(t, p, db.CashDividend{
		InstrumentID:    instID,
		ExDate:          d(2024, 2, 9),
		PayDate:         &pay,
		RecordDate:      &rec,
		DeclarationDate: &decl,
		Amount:          "0.24",
		Currency:        "USD",
		Frequency:       "quarterly",
		Type:            "SC",
		DataProvider:    "test",
		FirstKnownAt:    time.Date(2024, time.February, 2, 8, 0, 0, 0, time.UTC),
	})

	rows, err := p.ListCashDividendsForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Ref.Type != "MIC_TICKER" || r.Ref.Value != "AAPL" {
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
	// Type distinguishes a special cash dividend from a regular one, and is
	// what routes special dividends to unhandled events on re-import.
	if r.Type != "SC" {
		t.Errorf("type: want SC, got %q", r.Type)
	}
	want := time.Date(2024, time.February, 2, 8, 0, 0, 0, time.UTC)
	if !r.FirstKnownAt.Equal(want) {
		t.Errorf("first known at: want %s, got %s", want, r.FirstKnownAt)
	}
}

// TestListStockSplitsForExport_ExcludesInstrumentsWithoutIdentifiers verifies
// that an instrument with no identifiers does not appear in export output.
func TestListStockSplitsForExport_ExcludesInstrumentsWithoutIdentifiers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Insert a bare instrument (no identifiers) directly. EnsureInstrument
	// requires at least one identifier, so we side-step it. It gets no listing:
	// nothing states a currency, so there is no line to mint.
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

	open, high, low := decf(380), decf(385), decf(378)
	vol := int64(123456)
	if err := p.UpsertPrices(ctx, []db.EODPrice{{
		ListingID:    pricedListing(t, p, instID),
		PriceDate:    d(2024, 3, 1),
		Open:         &open,
		High:         &high,
		Low:          &low,
		Close:        decf(382.5),
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
		FROM eod_prices WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&saOpen, &saHigh, &saLow, &saClose, &saVolume); err != nil {
		t.Fatalf("read: %v", err)
	}
	if saOpen != 380 || saHigh != 385 || saLow != 378 || saClose != 382.5 || saVolume != 123456 {
		t.Errorf("trigger did not seed adjusted=raw: got open=%v high=%v low=%v close=%v vol=%d",
			saOpen, saHigh, saLow, saClose, saVolume)
	}
}

func TestApplyOptionSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create underlying stock instrument.
	underlyingID := setupInstrument(t, p, "AAPL-UNDERLYING")

	// Create option instrument with OCC identifier and option fields.
	expiry := d(2025, 1, 17)
	optFields := &db.OptionFields{Strike: decf(150), Expiry: expiry, PutCall: "C"}
	optID, _, err := p.EnsureInstrument(ctx, "OPTION", "USD", "AAPL 250117C00150000", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00150000"},
			Canonical: true,
		}}, nil, lineOf(t, p, underlyingID), optFields, "")
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}

	// Insert a transaction for the option so RecomputeSplitAdjustments has
	// something to adjust.
	userID := setupUser(t, p)
	insertTxs(t, p, userID, optID, []*apiv1.Tx{{
		BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType:        typev1.TxType_TRADE_ASSET,
		OrderDate:             ts(2024, 6, 1),
		TradeDate:             ts(2024, 6, 1),
		Quantity:              "1",
		UnitPrice:             proto.String("150"),
		InstrumentDescription: "AAPL 250117C00150000",
	}})

	// Insert the 4:1 split on the underlying (split_factor_at looks up
	// splits via underlying_id, not on the option instrument itself).
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{{
		InstrumentID: underlyingID,
		ExDate:       d(2024, 7, 1),
		SplitFrom:    "1",
		SplitTo:      "4",
		DataProvider: "test",
	}}); err != nil {
		t.Fatalf("upsert underlying split: %v", err)
	}

	// Restate the option: closes the old symbol at the ex_date, mints the
	// adjusted one from it, moves the strike, recomputes adjustments.
	params := db.OptionSplitParams{
		InstrumentID: optID,
		OldOCCValue:  "AAPL250117C00150000",
		Mints: []db.OptionMint{{
			ExDate: d(2024, 7, 1),
			OCC: db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00037500"},
				Canonical: true,
			},
			Strike: decf(37.5),
		}},
	}
	if err := p.ApplyOptionSplit(ctx, params); err != nil {
		t.Fatalf("apply option split: %v", err)
	}

	// Both names are stored: the one the contract traded under before the
	// ex_date, closed at it, and the minted one open from it. A broker file
	// exported either side of the split resolves to this same contract.
	inst, err := p.GetInstrument(ctx, optID)
	if err != nil {
		t.Fatalf("get instrument: %v", err)
	}
	old := findIdentifier(inst, "OCC", "AAPL250117C00150000")
	if old == nil {
		t.Fatal("the pre-split OCC symbol was not retained")
	}
	if old.ValidBefore == nil || !old.ValidBefore.Equal(d(2024, 7, 1)) {
		t.Errorf("pre-split OCC valid_before = %v, want the ex_date 2024-07-01", old.ValidBefore)
	}
	minted := findIdentifier(inst, "OCC", "AAPL250117C00037500")
	if minted == nil {
		t.Fatal("the adjusted OCC symbol was not minted")
	}
	if minted.ValidFrom == nil || !minted.ValidFrom.Equal(d(2024, 7, 1)) {
		t.Errorf("minted OCC valid_from = %v, want the ex_date 2024-07-01", minted.ValidFrom)
	}
	if minted.ValidBefore != nil {
		t.Errorf("minted OCC valid_before = %v, want it left open", minted.ValidBefore)
	}

	// Verify strike updated.
	if inst.Strike == nil || inst.Strike.String() != "37.5" {
		t.Errorf("strike: got %v, want 37.5", inst.Strike)
	}

	// The name is derived, not written: the trigger picks the identifier still
	// in force, which is the minted one.
	if inst.Name == nil || *inst.Name != "AAPL250117C00037500" {
		t.Errorf("name: got %v, want AAPL250117C00037500", inst.Name)
	}

	// No derived split row — split_factor_at looks up the underlying's splits
	// via the underlying_id FK. Verify the option has no splits of its own.
	splits, err := p.ListStockSplits(ctx, optID)
	if err != nil {
		t.Fatalf("list splits: %v", err)
	}
	if len(splits) != 0 {
		t.Fatalf("expected 0 splits on option (underlying lookup used), got %d", len(splits))
	}

	// Verify split-adjusted tx values. The tx is before the split ex_date,
	// so factor = 4: adjusted_quantity = 1*4 = 4, adjusted_price = 150/4 = 37.5.
	var saQty, saPrice float64
	if err := p.q.QueryRowContext(ctx, `
		SELECT split_adjusted_quantity, split_adjusted_unit_price
		FROM txs WHERE instrument_id = $1::uuid
	`, optID).Scan(&saQty, &saPrice); err != nil {
		t.Fatalf("read adjusted txs: %v", err)
	}
	if !approxEq(saQty, 4.0) {
		t.Errorf("split_adjusted_quantity: got %v, want 4", saQty)
	}
	if !approxEq(saPrice, 37.5) {
		t.Errorf("split_adjusted_unit_price: got %v, want 37.5", saPrice)
	}

	// The minted name's valid_from is the ex_date, which is what takes the
	// option off the pending list.
	pending, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	for _, pd := range pending {
		if pd.Option.ID == optID {
			t.Error("option still pending after being restated")
		}
	}
}

// backdate rewrites a knowledge timestamp to a fixed past value so that a
// subsequent write either preserves it or is caught clobbering it. Tests run
// inside one transaction, where now() is frozen at transaction start, so
// comparing two now() values would never detect a clobber.
// upsertDividends files dividends and fails the test if any named no line. The
// tests that care about the unattributable path call UpsertCashDividends
// directly and read the rows it hands back.
func upsertDividends(t *testing.T, p *Postgres, dividends ...db.CashDividend) {
	t.Helper()
	unfiled, err := p.UpsertCashDividends(context.Background(), dividends)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(unfiled) != 0 {
		t.Fatalf("expected every dividend to name a line, %d did not: %+v", len(unfiled), unfiled)
	}
}

func backdate(t *testing.T, p *Postgres, query string, args ...any) time.Time {
	t.Helper()
	past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
	args = append([]any{past}, args...)
	if _, err := p.q.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	return past
}

func TestUpsertCashDividends_PreservesKnowledgeTime(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	div := db.CashDividend{
		InstrumentID: instID, ExDate: d(2024, 2, 9),
		Amount: "0.24", Currency: "USD", DataProvider: "massive",
	}
	upsertDividends(t, p, div)

	past := backdate(t, p,
		`UPDATE cash_dividends d SET first_known_at = $1
		 FROM instrument_listings l WHERE l.id = d.listing_id AND l.instrument_id = $2`, instID)

	// The provider revises the amount. When we first learned of the dividend
	// does not change just because its amount did.
	div.Amount = "0.25"
	div.DataProvider = "eodhd"
	upsertDividends(t, p, div)

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got))
	}
	if got[0].Amount != "0.25" {
		t.Errorf("amount not revised: %q", got[0].Amount)
	}
	if !got[0].FirstKnownAt.Equal(past) {
		t.Errorf("knowledge time moved on revision: want %s, got %s", past, got[0].FirstKnownAt)
	}
}

func TestCreateCorporateEventFetchBlock_PreservesFirstBlockedAt(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.CreateCorporateEventFetchBlock(ctx, instID, "massive", "404"); err != nil {
		t.Fatalf("create: %v", err)
	}
	past := backdate(t, p,
		`UPDATE corporate_event_fetch_blocks SET first_blocked_at = $1 WHERE instrument_id = $2`, instID)

	// Blocking again records a newer reason, not a newer first-blocked-at.
	if err := p.CreateCorporateEventFetchBlock(ctx, instID, "massive", "subscription limit"); err != nil {
		t.Fatalf("re-create: %v", err)
	}

	blocks, err := p.ListCorporateEventFetchBlocks(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Reason != "subscription limit" {
		t.Errorf("reason not updated: %q", blocks[0].Reason)
	}
	if !blocks[0].FirstBlockedAt.Equal(past) {
		t.Errorf("first blocked at moved on re-block: want %s, got %s", past, blocks[0].FirstBlockedAt)
	}
}

// adjustedClose reads the derived split-adjusted close for one price row.
func adjustedClose(t *testing.T, p *Postgres, instID string, priceDate time.Time) float64 {
	t.Helper()
	var v float64
	err := p.q.QueryRowContext(context.Background(), `
		SELECT split_adjusted_close FROM eod_prices
		WHERE listing_id = $1::uuid AND price_date = $2
	`, pricedListing(t, p, instID), priceDate).Scan(&v)
	if err != nil {
		t.Fatalf("read split_adjusted_close for %s: %v", priceDate.Format("2006-01-02"), err)
	}
	return v
}

// TestRecomputeSplitAdjustments_BackfilledPricesUseTheirOwnDate covers the
// ordinary case: a user imports years of history today, so every bar is
// fetched now. The plugins return as-traded bars, so a bar printed before a
// split is denominated in the pre-split share count no matter when we fetched
// it, and adjusting it must depend on its price_date rather than on the fetch.
func TestRecomputeSplitAdjustments_BackfilledPricesUseTheirOwnDate(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// A 4:1 split with bars either side of it. No backdating: these rows are
	// fetched now, exactly as a backfill leaves them.
	insertPriceFull(t, p, instID, d(2020, 8, 28), 498, 500, 495, 499, 1_000_000, "test")
	insertPriceFull(t, p, instID, d(2020, 9, 1), 124, 126, 123, 125, 4_000_000, "test")

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	if got, want := adjustedClose(t, p, instID, d(2020, 8, 28)), 499.0/4; !approxEq(got, want) {
		t.Errorf("pre-split bar not adjusted: got %v want %v", got, want)
	}
	// Already in today's share count; adjusting it again would be wrong.
	if got, want := adjustedClose(t, p, instID, d(2020, 9, 1)), 125.0; !approxEq(got, want) {
		t.Errorf("post-split bar should be unchanged: got %v want %v", got, want)
	}
}

// TestRecomputeSplitAdjustments_BackAdjustedSourceDeclaresItsBasis is the
// mirror of the as-traded case: a provider that restates its whole series to
// the share count current when it answered has already applied the split, so
// adjusting again would double-count it.
func TestRecomputeSplitAdjustments_BackAdjustedSourceDeclaresItsBasis(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Same bar as the as-traded case, but already expressed post-split by the
	// provider, which declares that its series is current as of today.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if err := p.UpsertPrices(ctx, []db.EODPrice{{
		ListingID:       pricedListing(t, p, instID),
		PriceDate:       d(2020, 8, 28),
		Close:           decf(124.75),
		DataProvider:    "test",
		ShareCountBasis: &today,
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	if got := adjustedClose(t, p, instID, d(2020, 8, 28)); !approxEq(got, 124.75) {
		t.Errorf("back-adjusted bar adjusted a second time: got %v want %v", got, 124.75)
	}
}

// TestUpsertPrices_BasisTravelsWithTheRawValues covers a re-fetch replacing a
// row: the basis must be restated together with the values it describes, or
// the row is left claiming a denomination its numbers are not in.
func TestUpsertPrices_BasisTravelsWithTheRawValues(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	today := time.Now().UTC().Truncate(24 * time.Hour)
	row := db.EODPrice{
		ListingID: pricedListing(t, p, instID), PriceDate: d(2020, 8, 28),
		Close: decf(499), DataProvider: "test",
	}
	if err := p.UpsertPrices(ctx, []db.EODPrice{row}); err != nil {
		t.Fatalf("upsert as-traded: %v", err)
	}
	assertBasis(t, p, instID, d(2020, 8, 28), d(2020, 8, 28))

	// The same date re-fetched from a back-adjusting source.
	row.Close = decf(124.75)
	row.ShareCountBasis = &today
	if err := p.UpsertPrices(ctx, []db.EODPrice{row}); err != nil {
		t.Fatalf("upsert back-adjusted: %v", err)
	}
	assertBasis(t, p, instID, d(2020, 8, 28), today)
}

func assertBasis(t *testing.T, p *Postgres, instID string, priceDate, want time.Time) {
	t.Helper()
	var got time.Time
	if err := p.q.QueryRowContext(context.Background(), `
		SELECT share_count_basis FROM eod_prices
		WHERE listing_id = $1::uuid AND price_date = $2
	`, pricedListing(t, p, instID), priceDate).Scan(&got); err != nil {
		t.Fatalf("read share_count_basis: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("share_count_basis: got %s want %s",
			got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// TestRecomputeSplitAdjustments_TxsUseDeclaredBasis covers the two transaction
// sources. A broker log line is as-traded -- it accounts only for events prior
// to the trade -- so a pre-split buy is adjusted. A source that restates its
// history into current share terms declares that, and must not be adjusted
// again.
func TestRecomputeSplitAdjustments_TxsUseDeclaredBasis(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "AAPL")

	asTraded := insertTxWithBasis(t, p, userID, instID, d(2020, 8, 28), "100", nil)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	restated := insertTxWithBasis(t, p, userID, instID, d(2020, 8, 28), "400", &today)

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: instID, ExDate: d(2020, 8, 31), SplitFrom: "1", SplitTo: "4", DataProvider: "test"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	if got := adjustedQty(t, p, asTraded); !approxEq(got, 400) {
		t.Errorf("as-traded tx: got %v want %v", got, 400.0)
	}
	if got := adjustedQty(t, p, restated); !approxEq(got, 400) {
		t.Errorf("restated tx adjusted a second time: got %v want %v", got, 400.0)
	}
}

func insertTxWithBasis(t *testing.T, p *Postgres, userID, instID string, ts time.Time, qty string, basis *time.Time) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
			broker_tx_type, resolved_tx_type, quantity, instrument_id, share_count_basis,
			weight, weight_commodity, group_id)
		VALUES ($1::uuid, 'IBKR', '', $2, $2, 'AAPL', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', $3, $4::uuid, $5::date,
			$3, 'inst:' || $4, $6::uuid)
		RETURNING id
	`, userID, ts, qty, instID, basis, newTxGroup(t, p, userID)).Scan(&id)
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}
	return id
}

func adjustedQty(t *testing.T, p *Postgres, txID string) float64 {
	t.Helper()
	var v float64
	if err := p.q.QueryRowContext(context.Background(),
		`SELECT split_adjusted_quantity FROM txs WHERE id = $1::uuid`, txID).Scan(&v); err != nil {
		t.Fatalf("read split_adjusted_quantity: %v", err)
	}
	return v
}

// setupOption creates an option whose OCC symbol became correct on validFrom. A
// nil validFrom leaves the bound NULL, meaning the name predates everything
// known about it.
//
// underlyingID names the security; the option is written on its only line, which
// is the currency its strike is quoted in.
func setupOption(t *testing.T, p *Postgres, underlyingID, occ string, strike float64, validFrom *time.Time) string {
	t.Helper()
	ctx := context.Background()
	expiry, ok := derivative.OCCExpiry(occ)
	if !ok {
		t.Fatalf("setupOption: unparseable OCC %q", occ)
	}
	optFields := &db.OptionFields{Strike: decf(strike), Expiry: expiry, PutCall: "C"}
	id, _, err := p.EnsureInstrument(ctx, "OPTION", "USD", occ, "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "OCC", Value: occ},
			Canonical: true,
			ValidFrom: validFrom,
		}}, nil, lineOf(t, p, underlyingID), optFields, "")
	if err != nil {
		t.Fatalf("ensure option %s: %v", occ, err)
	}
	return id
}

// TestListPendingOptionSplits_Predicate covers the rule that decides whether an
// option still needs restating. The comparison is the OCC symbol's own
// valid_from against ex_date, never against a knowledge time: providers list the
// pre-split symbol until the ex_date, so a name that became correct before then
// does not reflect the split however long the split had been known. See
// docs/adr/0055-identifier-validity-is-an-interval.md.
//
// The expiry cases are the other half of the rule: a split only restates the
// contracts listed on its effective date, and a contract expiring that day is
// one of them. See docs/adr/0036-expired-options-are-not-restated.md.
//
// A NULL valid_from falls back to the option's first trade date, because a
// source names an option under the symbol current at its export and an export
// cannot precede the purchase it describes.
func TestListPendingOptionSplits_Predicate(t *testing.T) {
	past := d(2024, 6, 1)
	tests := []struct {
		name        string
		occ         string // empty uses an option expiring well after every ex_date
		validFrom   *time.Time
		firstTrade  *time.Time // a tx on the option, for the NULL valid_from fallback
		exDate      time.Time
		wantPending bool
	}{
		{
			name:        "name became correct before ex_date",
			validFrom:   timePtrCE(d(2024, 1, 1)),
			exDate:      past,
			wantPending: true,
		},
		{
			name:        "name became correct after ex_date",
			validFrom:   timePtrCE(d(2024, 12, 1)),
			exDate:      past,
			wantPending: false,
		},
		{
			name:        "name became correct on ex_date is already restated",
			validFrom:   timePtrCE(past),
			exDate:      past,
			wantPending: false,
		},
		{
			name:        "no valid_from and no trades predates every split",
			exDate:      past,
			wantPending: true,
		},
		{
			name:        "no valid_from falls back to a first trade before ex_date",
			firstTrade:  timePtrCE(d(2024, 1, 1)),
			exDate:      past,
			wantPending: true,
		},
		{
			name:        "no valid_from falls back to a first trade after ex_date",
			firstTrade:  timePtrCE(d(2024, 12, 1)),
			exDate:      past,
			wantPending: false,
		},
		{
			name:        "future-dated split is not yet effective",
			validFrom:   timePtrCE(d(2024, 1, 1)),
			exDate:      time.Now().UTC().AddDate(1, 0, 0).Truncate(24 * time.Hour),
			wantPending: false,
		},
		{
			name:        "option expired before ex_date was never restated",
			occ:         "AAPL240315C00200000",
			validFrom:   timePtrCE(d(2024, 1, 1)),
			exDate:      past,
			wantPending: false,
		},
		{
			name:        "option expiring on ex_date is restated",
			occ:         "AAPL240601C00200000",
			validFrom:   timePtrCE(d(2024, 1, 1)),
			exDate:      past,
			wantPending: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := testDBTx(t)
			ctx := context.Background()

			occ := tc.occ
			if occ == "" {
				occ = "AAPL250117C00200000"
			}
			underlyingID := setupInstrument(t, p, "PRED-UNDERLYING")
			optID := setupOption(t, p, underlyingID, occ, 200, tc.validFrom)
			if tc.firstTrade != nil {
				userID := setupUser(t, p)
				insertTxs(t, p, userID, optID, []*apiv1.Tx{{
					BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
					ResolvedTxType:        typev1.TxType_TRADE_ASSET,
					OrderDate:             ts(tc.firstTrade.Year(), tc.firstTrade.Month(), tc.firstTrade.Day()),
					TradeDate:             ts(tc.firstTrade.Year(), tc.firstTrade.Month(), tc.firstTrade.Day()),
					Quantity:              "1",
					UnitPrice:             proto.String("200"),
					InstrumentDescription: occ,
				}})
			}
			if err := p.UpsertStockSplits(ctx, []db.StockSplit{
				{InstrumentID: underlyingID, ExDate: tc.exDate, SplitFrom: "1", SplitTo: "2", DataProvider: "massive"},
			}); err != nil {
				t.Fatalf("upsert split: %v", err)
			}

			pending, err := p.ListPendingOptionSplits(ctx, "")
			if err != nil {
				t.Fatalf("ListPendingOptionSplits: %v", err)
			}
			found := false
			for _, pd := range pending {
				if pd.Option.ID == optID {
					found = true
				}
			}
			if found != tc.wantPending {
				t.Errorf("pending = %v, want %v", found, tc.wantPending)
			}
		})
	}
}

// TestListPendingOptionSplits_KnownBeforeButNotYetEffective is the regression
// test for issue 0055 at the query level. The split was known long before the
// option was identified, but did not take effect until afterwards, so the stored
// OCC is the pre-split one and the option is still pending. The old guard
// compared first_known_at and skipped it permanently.
func TestListPendingOptionSplits_KnownBeforeButNotYetEffective(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "KNOWN-EARLY")
	optID := setupOption(t, p, underlyingID, "AAPL250117C00200000", 200, timePtrCE(d(2024, 3, 1)))

	// Learned in January, effective in June, the name derived in March.
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{{
		InstrumentID: underlyingID,
		ExDate:       d(2024, 6, 1),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "massive",
		FirstKnownAt: d(2024, 1, 5),
	}}); err != nil {
		t.Fatalf("upsert split: %v", err)
	}

	pending, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("ListPendingOptionSplits: %v", err)
	}
	if len(pending) != 1 || pending[0].Option.ID != optID {
		t.Fatalf("pending = %v, want the option to still need adjusting", pending)
	}
}

// TestListPendingOptionSplits_ExpiredBeforeSplit is the regression test for
// issue 0058, in the shape that made it reachable: a pre-split price file dates
// the name it supplies before the ex_date by design, which is how genuinely
// restated contracts get picked up. The two NVDA puts expired in March and the split took
// effect in June, so OCC never restated them and the pass must leave strikes 420
// and 510 alone. The option expiring after the split is the control.
func TestListPendingOptionSplits_ExpiredBeforeSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "NVDA-UNDERLYING")
	priceFile := d(2024, 5, 1) // exported_at of a pre-split price file
	setupOption(t, p, underlyingID, "NVDA240315P00420000", 420, &priceFile)
	setupOption(t, p, underlyingID, "NVDA240315P00510000", 510, &priceFile)
	liveID := setupOption(t, p, underlyingID, "NVDA250117P00420000", 420, &priceFile)

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{{
		InstrumentID: underlyingID,
		ExDate:       d(2024, 6, 10),
		SplitFrom:    "1",
		SplitTo:      "10",
		DataProvider: "massive",
	}}); err != nil {
		t.Fatalf("upsert split: %v", err)
	}

	pending, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("ListPendingOptionSplits: %v", err)
	}
	if len(pending) != 1 || pending[0].Option.ID != liveID {
		t.Fatalf("pending = %v, want only the option that outlived the split", pending)
	}
}

// TestListPendingOptionSplits_SplitsBoundedByExpiry covers the guard applying
// per split rather than per option: an option that lived through the first split
// and expired before the second is pending for the first alone, so the pass
// compounds only that one.
func TestListPendingOptionSplits_SplitsBoundedByExpiry(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "BOUNDED-UNDERLYING")
	optID := setupOption(t, p, underlyingID, "AAPL240315C00400000", 400, nil)

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: underlyingID, ExDate: d(2024, 1, 10), SplitFrom: "1", SplitTo: "2", DataProvider: "massive"},
		{InstrumentID: underlyingID, ExDate: d(2024, 6, 10), SplitFrom: "1", SplitTo: "5", DataProvider: "massive"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}

	pending, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("ListPendingOptionSplits: %v", err)
	}
	if len(pending) != 1 || pending[0].Option.ID != optID {
		t.Fatalf("pending = %v, want the option pending for one split", pending)
	}
	if len(pending[0].Splits) != 1 || !pending[0].Splits[0].ExDate.Equal(d(2024, 1, 10)) {
		t.Errorf("splits = %v, want only the 2024-01-10 split", pending[0].Splits)
	}
}

// TestListPendingOptionSplits_MultipleAndFilter covers grouping, ordering, and
// the underlying filter. Splits come back oldest first so the pass can compound
// them, and the option arrives with its identifiers loaded so the pass can find
// the OCC symbol.
func TestListPendingOptionSplits_MultipleAndFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	undA := setupInstrument(t, p, "MULTI-A")
	undB := setupInstrument(t, p, "MULTI-B")
	optA := setupOption(t, p, undA, "AAPL250117C00400000", 400, nil)
	setupOption(t, p, undB, "MSFT250117C00300000", 300, nil)

	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: undA, ExDate: d(2024, 9, 1), SplitFrom: "1", SplitTo: "4", DataProvider: "massive"},
		{InstrumentID: undA, ExDate: d(2024, 6, 1), SplitFrom: "1", SplitTo: "2", DataProvider: "massive"},
		{InstrumentID: undB, ExDate: d(2024, 7, 1), SplitFrom: "1", SplitTo: "3", DataProvider: "massive"},
	}); err != nil {
		t.Fatalf("upsert splits: %v", err)
	}

	all, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("ListPendingOptionSplits: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("pending options = %d, want 2", len(all))
	}

	filtered, err := p.ListPendingOptionSplits(ctx, undA)
	if err != nil {
		t.Fatalf("ListPendingOptionSplits filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Option.ID != optA {
		t.Fatalf("filtered = %v, want just the option on undA", filtered)
	}
	if len(filtered[0].Splits) != 2 {
		t.Fatalf("splits = %d, want 2", len(filtered[0].Splits))
	}
	if !filtered[0].Splits[0].ExDate.Equal(d(2024, 6, 1)) {
		t.Errorf("splits not ordered by ex_date ascending: %v", filtered[0].Splits[0].ExDate)
	}
	if filtered[0].Splits[0].InstrumentID != undA {
		t.Errorf("split instrument = %q, want the underlying %q", filtered[0].Splits[0].InstrumentID, undA)
	}
	var hasOCC bool
	for _, idn := range filtered[0].Option.Identifiers {
		if idn.Ref.Type == "OCC" {
			hasOCC = true
		}
	}
	if !hasOCC {
		t.Error("option identifiers not loaded; the pass needs the OCC symbol")
	}
}

// TestListPendingOptionSplits_ClearedByApply verifies the pass is idempotent:
// the minted name's valid_from is the ex_date, which is what removes the option
// from the work list, so a second run finds nothing.
func TestListPendingOptionSplits_ClearedByApply(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "IDEMPOTENT")
	optID := setupOption(t, p, underlyingID, "AAPL250117C00200000", 200, timePtrCE(d(2024, 1, 1)))
	if err := p.UpsertStockSplits(ctx, []db.StockSplit{
		{InstrumentID: underlyingID, ExDate: d(2024, 6, 1), SplitFrom: "1", SplitTo: "2", DataProvider: "massive"},
	}); err != nil {
		t.Fatalf("upsert split: %v", err)
	}

	pending, err := p.ListPendingOptionSplits(ctx, "")
	if err != nil || len(pending) != 1 {
		t.Fatalf("first run: pending = %v, err = %v", pending, err)
	}

	if err := p.ApplyOptionSplit(ctx, db.OptionSplitParams{
		InstrumentID: optID,
		OldOCCValue:  "AAPL250117C00200000",
		Mints: []db.OptionMint{{
			ExDate: d(2024, 6, 1),
			OCC: db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00100000"},
				Canonical: true,
			},
			Strike: decf(100),
		}},
	}); err != nil {
		t.Fatalf("ApplyOptionSplit: %v", err)
	}

	pending, err = p.ListPendingOptionSplits(ctx, "")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending after apply = %d, want 0", len(pending))
	}
}

// TestApplyOptionSplit_ConvergesOnStaleOldOCC covers two runs of the pass
// overlapping on the same option. The pass is called from both the corporate
// event fetch cycle and the corporate event import job, so a run can compute its
// restatement from a snapshot another run has already superseded.
//
// Run A sees only the 2:1 at 06-01 and mints 200 -> 100 from it. The 4:1 at
// 09-01 then lands, so run B sees both and mints 100 from 06-01 and 25 from
// 09-01, from the same starting strike. A commits first, so B's first mint lands
// on a window A has already written -- and on a row starting the very day B is
// closing at, which would be a zero-length interval. B owns that window, so it
// clears it and rewrites it, leaving one history consistent with the strike.
func TestApplyOptionSplit_ConvergesOnStaleOldOCC(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "RACE")
	optID := setupOption(t, p, underlyingID, "AAPL250117C00200000", 200, timePtrCE(d(2024, 1, 1)))

	// Run A commits first: 2:1 only.
	if err := p.ApplyOptionSplit(ctx, db.OptionSplitParams{
		InstrumentID: optID,
		OldOCCValue:  "AAPL250117C00200000",
		Mints: []db.OptionMint{{
			ExDate: d(2024, 6, 1),
			OCC: db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00100000"},
				Canonical: true,
			},
			Strike: decf(100),
		}},
	}); err != nil {
		t.Fatalf("run A: %v", err)
	}

	// Run B commits second, still carrying the pre-A symbol it read.
	if err := p.ApplyOptionSplit(ctx, db.OptionSplitParams{
		InstrumentID: optID,
		OldOCCValue:  "AAPL250117C00200000", // stale
		Mints: []db.OptionMint{
			{
				ExDate: d(2024, 6, 1),
				OCC: db.IdentifierInput{
					Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00100000"},
					Canonical: true,
				},
				Strike: decf(100),
			},
			{
				ExDate: d(2024, 9, 1),
				OCC: db.IdentifierInput{
					Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00025000"},
					Canonical: true,
				},
				Strike: decf(25),
			},
		},
	}); err != nil {
		t.Fatalf("run B: %v", err)
	}

	inst, err := p.GetInstrument(ctx, optID)
	if err != nil || inst == nil {
		t.Fatalf("get instrument: %v", err)
	}
	assertOCCHistory(t, inst, []occSpan{
		{"AAPL250117C00200000", timePtrCE(d(2024, 1, 1)), timePtrCE(d(2024, 6, 1))},
		{"AAPL250117C00100000", timePtrCE(d(2024, 6, 1)), timePtrCE(d(2024, 9, 1))},
		{"AAPL250117C00025000", timePtrCE(d(2024, 9, 1)), nil},
	})
	if inst.Strike == nil || inst.Strike.String() != "25" {
		t.Errorf("strike = %v, want 25 to match the symbol in force", inst.Strike)
	}
}

// TestApplyOptionSplit_AbsorbsDuplicateHoldingTheMintedName covers the option
// being bought again, or its prices fetched, while the split was still unknown:
// the post-split symbol resolved to nothing and was given an instrument of its
// own. Minting that same name now collides with it.
//
// Failing would roll the restatement back and leave the option pending, and
// every later cycle would collide the same way. So the duplicate is absorbed
// into the contract being restated, which is the row carrying its history.
func TestApplyOptionSplit_AbsorbsDuplicateHoldingTheMintedName(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	underlyingID := setupInstrument(t, p, "ABSORB")
	optID := setupOption(t, p, underlyingID, "AAPL250117C00200000", 200, timePtrCE(d(2024, 1, 1)))

	// The duplicate: same contract, named by the symbol it wears after the
	// split, created before the split was known about.
	dupID := setupOption(t, p, underlyingID, "AAPL250117C00100000", 100, timePtrCE(d(2024, 7, 1)))
	if dupID == optID {
		t.Fatal("the duplicate should be its own instrument")
	}
	userID := setupUser(t, p)
	insertTxs(t, p, userID, dupID, []*apiv1.Tx{{
		BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
		ResolvedTxType:        typev1.TxType_TRADE_ASSET,
		OrderDate:             ts(2024, 8, 1),
		TradeDate:             ts(2024, 8, 1),
		Quantity:              "1",
		UnitPrice:             proto.String("100"),
		InstrumentDescription: "AAPL250117C00100000",
	}})

	if err := p.ApplyOptionSplit(ctx, db.OptionSplitParams{
		InstrumentID: optID,
		OldOCCValue:  "AAPL250117C00200000",
		Mints: []db.OptionMint{{
			ExDate: d(2024, 6, 1),
			OCC: db.IdentifierInput{
				Ref:       db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00100000"},
				Canonical: true,
			},
			Strike: decf(100),
		}},
	}); err != nil {
		t.Fatalf("apply option split: %v", err)
	}

	if gone, err := p.GetInstrument(ctx, dupID); err == nil && gone != nil {
		t.Error("the duplicate survived; it should have been absorbed")
	}

	inst, err := p.GetInstrument(ctx, optID)
	if err != nil || inst == nil {
		t.Fatalf("get instrument: %v", err)
	}
	assertOCCHistory(t, inst, []occSpan{
		{"AAPL250117C00200000", timePtrCE(d(2024, 1, 1)), timePtrCE(d(2024, 6, 1))},
		{"AAPL250117C00100000", timePtrCE(d(2024, 6, 1)), nil},
	})

	// The duplicate's transaction came with it, or absorbing it would have lost
	// a holding.
	var n int
	if err := p.q.QueryRowContext(ctx, `SELECT count(*) FROM txs WHERE instrument_id = $1::uuid`, optID).Scan(&n); err != nil {
		t.Fatalf("count txs: %v", err)
	}
	if n != 1 {
		t.Errorf("txs on the survivor = %d, want the duplicate's 1", n)
	}
}

// occSpan is one expected OCC row: the symbol and the interval it names the
// contract over.
type occSpan struct {
	value                  string
	validFrom, validBefore *time.Time
}

// assertOCCHistory checks the option's OCC rows are exactly the given spans, in
// order. A restated contract wears one name at a time and the spans abut, so
// anything else is a history with a hole or an overlap in it.
func assertOCCHistory(t *testing.T, inst *db.InstrumentRow, want []occSpan) {
	t.Helper()
	var got []occSpan
	for _, idn := range inst.Identifiers {
		if idn.Ref.Type == "OCC" {
			got = append(got, occSpan{idn.Ref.Value, idn.ValidFrom, idn.ValidBefore})
		}
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].validFrom == nil {
			return got[j].validFrom != nil
		}
		if got[j].validFrom == nil {
			return false
		}
		return got[i].validFrom.Before(*got[j].validFrom)
	})
	if len(got) != len(want) {
		t.Fatalf("OCC rows = %v, want %v", fmtSpans(got), fmtSpans(want))
	}
	for i := range want {
		if got[i].value != want[i].value ||
			!eqDate(got[i].validFrom, want[i].validFrom) ||
			!eqDate(got[i].validBefore, want[i].validBefore) {
			t.Errorf("OCC row %d = %s, want %s", i, fmtSpan(got[i]), fmtSpan(want[i]))
		}
	}
}

func eqDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func fmtSpan(s occSpan) string {
	f, b := "-", "-"
	if s.validFrom != nil {
		f = s.validFrom.Format("2006-01-02")
	}
	if s.validBefore != nil {
		b = s.validBefore.Format("2006-01-02")
	}
	return fmt.Sprintf("%s[%s,%s)", s.value, f, b)
}

func fmtSpans(ss []occSpan) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmtSpan(s)
	}
	return strings.Join(out, " ")
}

func timePtrCE(t time.Time) *time.Time { return &t }

// Coverage is stored per (instrument, plugin), but an import records every span
// as data_provider = "import", so the export merges across plugins.
func TestListCorporateEventCoverageForExport_MergesAcrossPlugins(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupTickerInstrument(t, p, "AAPL")
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "massive", d(2020, 1, 1), d(2022, 1, 1), nil); err != nil {
		t.Fatalf("upsert coverage massive: %v", err)
	}
	if err := p.UpsertCorporateEventCoverage(ctx, instID, "eodhd", d(2022, 1, 1), d(2024, 1, 1), nil); err != nil {
		t.Fatalf("upsert coverage eodhd: %v", err)
	}

	cov, err := p.ListCorporateEventCoverageForExport(ctx)
	if err != nil {
		t.Fatalf("list corporate event coverage for export: %v", err)
	}
	if len(cov) != 1 {
		t.Fatalf("expected the two adjacent spans merged into 1, got %d", len(cov))
	}
	if !cov[0].From.Equal(d(2020, 1, 1)) || !cov[0].Before.Equal(d(2024, 1, 1)) {
		t.Errorf("expected [2020-01-01, 2024-01-01), got [%s, %s)",
			cov[0].From.Format("2006-01-02"), cov[0].Before.Format("2006-01-02"))
	}
	if cov[0].Ref.Type != "MIC_TICKER" || cov[0].Ref.Value != "AAPL" {
		t.Errorf("got identifier %s %s", cov[0].Ref.Type, cov[0].Ref.Value)
	}
}

func TestListCorporateEventCoverageForExport_Empty(t *testing.T) {
	p := testDBTx(t)
	cov, err := p.ListCorporateEventCoverageForExport(context.Background())
	if err != nil {
		t.Fatalf("list corporate event coverage for export: %v", err)
	}
	if len(cov) != 0 {
		t.Fatalf("expected no spans, got %d", len(cov))
	}
}

// setupDualLineInstrument makes a security quoted in two currencies, which is
// the shape the security-grain dividend key could not represent.
func setupDualLineInstrument(t *testing.T, p *Postgres, isin string) (instID, usdLine, gbpLine string) {
	t.Helper()
	ctx := context.Background()
	instID, usdLine, err := p.EnsureInstrument(ctx, "STOCK", "USD", "DUAL", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: isin}, Canonical: true}}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	gbpLine, err = p.EnsureListing(ctx, instID, "GBP")
	if err != nil {
		t.Fatalf("ensure GBP line: %v", err)
	}
	if usdLine == "" || gbpLine == "" || usdLine == gbpLine {
		t.Fatalf("fixture wants two distinct lines, got %q and %q", usdLine, gbpLine)
	}
	return instID, usdLine, gbpLine
}

// This is the collision the security-grain key lost. Keyed on
// (instrument_id, ex_date), the second of these two overwrote the first and the
// GBP line's payment vanished.
func TestUpsertCashDividends_OneExDatePaysInTwoCurrencies(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, usdLine, gbpLine := setupDualLineInstrument(t, p, "GB0000000DL1")

	ex := d(2024, 2, 9)
	upsertDividends(t, p,
		db.CashDividend{InstrumentID: instID, ExDate: ex, Amount: "0.24", Currency: "USD", DataProvider: "massive"},
		db.CashDividend{InstrumentID: instID, ExDate: ex, Amount: "0.19", Currency: "GBP", DataProvider: "eodhd"},
	)

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both payments, got %d: %+v", len(got), got)
	}
	byLine := map[string]db.CashDividend{}
	for _, g := range got {
		byLine[g.ListingID] = g
	}
	if g := byLine[usdLine]; g.Amount != "0.24" || g.Currency != "USD" {
		t.Errorf("USD line: %+v", g)
	}
	if g := byLine[gbpLine]; g.Amount != "0.19" || g.Currency != "GBP" {
		t.Errorf("GBP line: %+v", g)
	}
}

// The join is on the currency family, so a provider quoting the London line's
// dividend in pence files against the line stored as GBP rather than naming no
// line. The amount keeps the code it was quoted in: 19 pence is not 19 pounds.
func TestUpsertCashDividends_PenceFilesAgainstThePoundLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, _, gbpLine := setupDualLineInstrument(t, p, "GB0000000DL2")

	upsertDividends(t, p, db.CashDividend{
		InstrumentID: instID, ExDate: d(2024, 5, 2),
		Amount: "19", Currency: "GBX", DataProvider: "eodhd",
	})

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got))
	}
	if got[0].ListingID != gbpLine {
		t.Errorf("line: got %s, want the GBP line %s", got[0].ListingID, gbpLine)
	}
	if got[0].Currency != "GBX" || got[0].Amount != "19" {
		t.Errorf("the quoted code and amount must survive: %+v", got[0])
	}
}

// A dividend reported in a currency the security is not quoted in says a payment
// was made, not that the security has a line there -- a broker converting into
// the account currency reports exactly that. It is handed back rather than
// stored, and rather than minting a line nobody asserted. See
// docs/adr/0073-a-dividend-names-a-line-it-does-not-mint.md.
func TestUpsertCashDividends_UnattributableCurrencyMintsNothing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID, _, _ := setupDualLineInstrument(t, p, "GB0000000DL3")

	unfiled, err := p.UpsertCashDividends(ctx, []db.CashDividend{
		{InstrumentID: instID, ExDate: d(2024, 2, 9), Amount: "0.24", Currency: "USD", DataProvider: "massive"},
		{InstrumentID: instID, ExDate: d(2024, 2, 9), Amount: "0.31", Currency: "CAD", DataProvider: "broker"},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if len(unfiled) != 1 {
		t.Fatalf("expected the CAD dividend back, got %d: %+v", len(unfiled), unfiled)
	}
	if unfiled[0].Currency != "CAD" || unfiled[0].Amount != "0.31" {
		t.Errorf("wrong row handed back: %+v", unfiled[0])
	}
	if unfiled[0].InstrumentID != instID {
		t.Errorf("instrument: got %s, want %s", unfiled[0].InstrumentID, instID)
	}

	got, err := p.ListCashDividends(ctx, instID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Currency != "USD" {
		t.Errorf("only the attributable dividend should be stored, got %+v", got)
	}
	var lines int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM instrument_listings WHERE instrument_id = $1`, instID).Scan(&lines); err != nil {
		t.Fatalf("count listings: %v", err)
	}
	if lines != 2 {
		t.Errorf("listing count: got %d, want the 2 it started with -- a dividend mints no line", lines)
	}
}

// A dividend is stored against a line, so a merge has to move it or the loser's
// listings cascade it away. It lands on the survivor's line of the same currency
// family, as the postings do.
func TestMergeInstruments_DividendMovesToTheSurvivorsLine(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	survivor, survivorLine, err := p.EnsureInstrument(ctx, "STOCK", "USD", "S", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000DV1"}, Canonical: true}}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	loser, _, err := p.EnsureInstrument(ctx, "STOCK", "USD", "L", "", "", []db.IdentifierInput{
		{Ref: db.InstrumentRef{Type: "ISIN", Value: "US0000000DV2"}, Canonical: true}}, nil, "", nil, "")
	if err != nil {
		t.Fatalf("ensure loser: %v", err)
	}
	// The survivor already holds the 2024-02-09 payment, so the loser's copy of
	// it is superseded rather than moved; its 2024-05-09 one has nothing to
	// collide with and travels.
	upsertDividends(t, p,
		db.CashDividend{InstrumentID: survivor, ExDate: d(2024, 2, 9), Amount: "0.24", Currency: "USD", DataProvider: "massive"},
		db.CashDividend{InstrumentID: loser, ExDate: d(2024, 2, 9), Amount: "0.24", Currency: "USD", DataProvider: "eodhd"},
		db.CashDividend{InstrumentID: loser, ExDate: d(2024, 5, 9), Amount: "0.25", Currency: "USD", DataProvider: "eodhd"},
	)

	if err := p.runInTx(ctx, func(exec queryable) error {
		return mergeInstruments(ctx, exec, uuid.MustParse(survivor), uuid.MustParse(loser))
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	got, err := p.ListCashDividends(ctx, survivor)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 dividends after the merge, got %d: %+v", len(got), got)
	}
	for _, g := range got {
		if g.ListingID != survivorLine {
			t.Errorf("dividend on %s names line %s, want the survivor's %s",
				g.ExDate.Format("2006-01-02"), g.ListingID, survivorLine)
		}
	}
	if got[0].DataProvider != "massive" {
		t.Errorf("the survivor's own row should win the collision, got %q", got[0].DataProvider)
	}
}
