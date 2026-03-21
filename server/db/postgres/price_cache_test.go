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

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func ts(year int, month time.Month, day int) *timestamppb.Timestamp {
	return timestamppb.New(d(year, month, day))
}

// setupUser creates a user and returns userID.
func setupUser(t *testing.T, p *Postgres) string {
	t.Helper()
	ctx := context.Background()
	id, err := p.GetOrCreateUser(ctx, "sub|price-test", "Test", "test@test.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

// setupInstrument creates an instrument with a broker description identifier.
func setupInstrument(t *testing.T, p *Postgres, desc string) string {
	t.Helper()
	ctx := context.Background()
	id, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "TEST", Value: desc, Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument %s: %v", desc, err)
	}
	return id
}

// insertTxs inserts transactions for a single instrument.
func insertTxs(t *testing.T, p *Postgres, userID, instID string, txs []*apiv1.Tx) {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, len(txs))
	for i := range ids {
		ids[i] = instID
	}
	from := timestamppb.New(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	to := timestamppb.New(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "TEST", from, to, txs, ids); err != nil {
		t.Fatalf("insert txs: %v", err)
	}
}

// insertPrice inserts a single eod_prices row.
func insertPrice(t *testing.T, p *Postgres, instID string, priceDate time.Time, close float64) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, close, data_provider)
		VALUES ($1::uuid, $2, $3, 'test')
	`, instID, priceDate, close)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
}

func assertInstrumentRanges(t *testing.T, got []db.InstrumentDateRanges, instID string, want []db.DateRange) {
	t.Helper()
	var found *db.InstrumentDateRanges
	for i := range got {
		if got[i].InstrumentID == instID {
			found = &got[i]
			break
		}
	}
	if want == nil {
		if found != nil {
			t.Errorf("instrument %s: expected no ranges, got %d", instID, len(found.Ranges))
		}
		return
	}
	if found == nil {
		t.Fatalf("instrument %s: not found in results", instID)
	}
	if len(found.Ranges) != len(want) {
		t.Fatalf("instrument %s: got %d ranges, want %d\ngot:  %v\nwant: %v",
			instID, len(found.Ranges), len(want), fmtRanges(found.Ranges), fmtRanges(want))
	}
	for i := range want {
		if !found.Ranges[i].From.Equal(want[i].From) || !found.Ranges[i].To.Equal(want[i].To) {
			t.Errorf("instrument %s range[%d]: got [%s, %s), want [%s, %s)",
				instID, i,
				found.Ranges[i].From.Format("2006-01-02"), found.Ranges[i].To.Format("2006-01-02"),
				want[i].From.Format("2006-01-02"), want[i].To.Format("2006-01-02"))
		}
	}
}

func fmtRanges(rs []db.DateRange) string {
	s := "["
	for i, r := range rs {
		if i > 0 {
			s += ", "
		}
		s += "[" + r.From.Format("2006-01-02") + ", " + r.To.Format("2006-01-02") + ")"
	}
	return s + "]"
}

// --- HeldRanges tests ---

func TestHeldRanges_BuySell(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "AAPL")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "A"},
		{Timestamp: ts(2024, 3, 15), InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -100, Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 3, 15)},
	})
}

func TestHeldRanges_OpenPosition(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GOOG")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 6, 1), InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 50, Account: "A"},
	})

	today := time.Now().UTC().Truncate(db.Day)

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{ExtendToToday: true})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 6, 1), To: today.Add(db.Day)},
	})
}

func TestHeldRanges_OpenPositionNoExtend(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "MSFT")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 6, 1), InstrumentDescription: "MSFT", Type: apiv1.TxType_BUYSTOCK, Quantity: 50, Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{ExtendToToday: false})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	// Without extend, open position just gets +1 day from range start.
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 6, 1), To: d(2024, 6, 2)},
	})
}

func TestHeldRanges_CloseAndReopen(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "TSLA")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "TSLA", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "A"},
		{Timestamp: ts(2024, 2, 15), InstrumentDescription: "TSLA", Type: apiv1.TxType_SELLSTOCK, Quantity: -100, Account: "A"},
		{Timestamp: ts(2024, 4, 1), InstrumentDescription: "TSLA", Type: apiv1.TxType_BUYSTOCK, Quantity: 50, Account: "A"},
		{Timestamp: ts(2024, 5, 1), InstrumentDescription: "TSLA", Type: apiv1.TxType_SELLSTOCK, Quantity: -50, Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 15)},
		{From: d(2024, 4, 1), To: d(2024, 5, 1)},
	})
}

func TestHeldRanges_Lookback(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "AMZN")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 3, 1), InstrumentDescription: "AMZN", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "A"},
		{Timestamp: ts(2024, 4, 1), InstrumentDescription: "AMZN", Type: apiv1.TxType_SELLSTOCK, Quantity: -100, Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{LookbackDays: 30})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	// held_from should be 2024-03-01 minus 30 days = 2024-01-31
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 31), To: d(2024, 4, 1)},
	})
}

func TestHeldRanges_LookbackMerge(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "META")

	// Two ranges close enough that lookback causes overlap and merge.
	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "META", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "A"},
		{Timestamp: ts(2024, 1, 20), InstrumentDescription: "META", Type: apiv1.TxType_SELLSTOCK, Quantity: -100, Account: "A"},
		{Timestamp: ts(2024, 2, 1), InstrumentDescription: "META", Type: apiv1.TxType_BUYSTOCK, Quantity: 50, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "META", Type: apiv1.TxType_SELLSTOCK, Quantity: -50, Account: "A"},
	})

	// Without lookback: two separate ranges.
	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 1, 20)},
		{From: d(2024, 2, 1), To: d(2024, 2, 10)},
	})

	// With 20-day lookback: second range's from (2024-02-01 - 20d = 2024-01-12) overlaps first,
	// so they merge.
	got, err = p.HeldRanges(ctx, db.HeldRangesOpts{LookbackDays: 20})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2023, 12, 21), To: d(2024, 2, 10)},
	})
}

func TestHeldRanges_UnidentifiedExcluded(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// Insert a tx with NULL instrument_id directly via SQL.
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity)
		VALUES ($1::uuid, 'TEST', 'A', $2, 'UNKNOWN', 'BUYSTOCK', 100)
	`, userID, d(2024, 6, 1))
	if err != nil {
		t.Fatalf("insert tx: %v", err)
	}

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{ExtendToToday: true})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no results for unidentified txs, got %d instruments", len(got))
	}
}

func TestHeldRanges_MultipleInstruments(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	inst1 := setupInstrument(t, p, "INST1")
	inst2 := setupInstrument(t, p, "INST2")

	// Insert txs for inst1.
	insertTxs(t, p, userID, inst1, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 1), InstrumentDescription: "INST1", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 1), InstrumentDescription: "INST1", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	// Insert txs for inst2 using CreateTx to avoid ReplaceTxsInPeriod conflict with same broker/period.
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 3, 1), InstrumentDescription: "INST2", Type: apiv1.TxType_BUYSTOCK, Quantity: 20, Account: "A",
	}, inst2); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 4, 1), InstrumentDescription: "INST2", Type: apiv1.TxType_SELLSTOCK, Quantity: -20, Account: "A",
	}, inst2); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 instruments, got %d", len(got))
	}
	assertInstrumentRanges(t, got, inst1, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 2, 1)},
	})
	assertInstrumentRanges(t, got, inst2, []db.DateRange{
		{From: d(2024, 3, 1), To: d(2024, 4, 1)},
	})
}

func TestHeldRanges_MultipleUsers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	user1, _ := p.GetOrCreateUser(ctx, "sub|user1", "U1", "u1@test.com")
	user2, _ := p.GetOrCreateUser(ctx, "sub|user2", "U2", "u2@test.com")
	instID := setupInstrument(t, p, "SHARED")

	// User 1 holds Jan-Feb.
	insertTxs(t, p, user1, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 1), InstrumentDescription: "SHARED", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 1), InstrumentDescription: "SHARED", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	// User 2 holds Mar-Apr (separate broker to avoid replace conflict).
	if err := p.CreateTx(ctx, user2, "TEST2", "B", &apiv1.Tx{
		Timestamp: ts(2024, 3, 1), InstrumentDescription: "SHARED", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: "B",
	}, instID); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, user2, "TEST2", "B", &apiv1.Tx{
		Timestamp: ts(2024, 4, 1), InstrumentDescription: "SHARED", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: "B",
	}, instID); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// System-wide: should see both ranges.
	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 2, 1)},
		{From: d(2024, 3, 1), To: d(2024, 4, 1)},
	})
}

func TestHeldRanges_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}

// --- PriceCoverage tests ---

func TestPriceCoverage_Contiguous(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "COV1")

	// Insert 5 contiguous days.
	for i := 0; i < 5; i++ {
		insertPrice(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), 100.0)
	}

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 1, 6)},
	})
}

func TestPriceCoverage_WithGap(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "COV2")

	// Jan 1-3, then Jan 10-12 (gap of 7 days).
	for i := 0; i < 3; i++ {
		insertPrice(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), 100.0)
	}
	for i := 0; i < 3; i++ {
		insertPrice(t, p, instID, d(2024, 1, 10).AddDate(0, 0, i), 100.0)
	}

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 1, 4)},
		{From: d(2024, 1, 10), To: d(2024, 1, 13)},
	})
}

func TestPriceCoverage_WeekendBridge(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "COV3")

	// Fri Jan 5, Mon Jan 8 (weekend gap = 2 calendar days).
	// range_agg treats them as separate since there's a gap between Jan 6 and Jan 8.
	insertPrice(t, p, instID, d(2024, 1, 5), 100.0)
	insertPrice(t, p, instID, d(2024, 1, 8), 100.0)

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	// range_agg: [Jan5, Jan6) and [Jan8, Jan9) are separate ranges.
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 5), To: d(2024, 1, 6)},
		{From: d(2024, 1, 8), To: d(2024, 1, 9)},
	})
}

func TestPriceCoverage_FilterByInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst1 := setupInstrument(t, p, "FILT1")
	inst2 := setupInstrument(t, p, "FILT2")

	insertPrice(t, p, inst1, d(2024, 1, 1), 100.0)
	insertPrice(t, p, inst2, d(2024, 2, 1), 200.0)

	// Filter to inst1 only.
	got, err := p.PriceCoverage(ctx, []string{inst1})
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 instrument, got %d", len(got))
	}
	assertInstrumentRanges(t, got, inst1, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 1, 2)},
	})
}

func TestPriceCoverage_SingleDay(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "COV4")

	insertPrice(t, p, instID, d(2024, 6, 15), 100.0)

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 6, 15), To: d(2024, 6, 16)},
	})
}

func TestPriceCoverage_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}

// --- PriceGaps tests ---

func TestPriceGaps_NoPrices(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPNONE")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "GAPNONE", Type: apiv1.TxType_BUYSTOCK, Quantity: 100, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "GAPNONE", Type: apiv1.TxType_SELLSTOCK, Quantity: -100, Account: "A"},
	})

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	// With no prices, gaps = entire held range.
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 10)},
	})
}

func TestPriceGaps_FullCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPFULL")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 1), InstrumentDescription: "GAPFULL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 1, 4), InstrumentDescription: "GAPFULL", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	// Insert prices covering [Jan 1, Jan 4) fully.
	for i := 0; i < 3; i++ {
		insertPrice(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), 100.0)
	}

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	// No gaps expected.
	assertInstrumentRanges(t, got, instID, nil)
}

func TestPriceGaps_PartialCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPPART")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 1), InstrumentDescription: "GAPPART", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "GAPPART", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	// Prices for Jan 3-5 only (gap before and after).
	for i := 2; i < 5; i++ {
		insertPrice(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), 100.0)
	}

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	assertInstrumentRanges(t, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 1, 3)},
		{From: d(2024, 1, 6), To: d(2024, 1, 10)},
	})
}

func TestPriceGaps_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}

// --- UpsertPrices tests ---

func TestUpsertPrices_Insert(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "UPS1")

	open := 100.0
	high := 105.0
	low := 99.0
	vol := int64(1000)
	err := p.UpsertPrices(ctx, []db.EODPrice{
		{
			InstrumentID: instID,
			PriceDate:    d(2024, 1, 1),
			Open:         &open,
			High:         &high,
			Low:          &low,
			Close:        102.0,
			Volume:       &vol,
			DataProvider: "test",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify via coverage.
	cov, err := p.PriceCoverage(ctx, []string{instID})
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	assertInstrumentRanges(t, cov, instID, []db.DateRange{
		{From: d(2024, 1, 1), To: d(2024, 1, 2)},
	})
}

func TestUpsertPrices_Overwrite(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "UPS2")

	// Insert initial price.
	insertPrice(t, p, instID, d(2024, 1, 1), 100.0)

	// Upsert with new close.
	err := p.UpsertPrices(ctx, []db.EODPrice{
		{
			InstrumentID: instID,
			PriceDate:    d(2024, 1, 1),
			Close:        200.0,
			DataProvider: "updated",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify updated value.
	var close float64
	var provider string
	err = p.q.QueryRowContext(ctx, `SELECT close, data_provider FROM eod_prices WHERE instrument_id = $1::uuid AND price_date = $2`, instID, d(2024, 1, 1)).Scan(&close, &provider)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if close != 200.0 {
		t.Errorf("close = %v, want 200.0", close)
	}
	if provider != "updated" {
		t.Errorf("data_provider = %q, want updated", provider)
	}
}

func TestUpsertPrices_NullableFields(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "UPS3")

	err := p.UpsertPrices(ctx, []db.EODPrice{
		{
			InstrumentID: instID,
			PriceDate:    d(2024, 1, 1),
			Close:        50.0,
			DataProvider: "test",
			// Open, High, Low, Volume all nil
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var open, high, low sql.NullFloat64
	var vol sql.NullInt64
	err = p.q.QueryRowContext(ctx, `SELECT open, high, low, volume FROM eod_prices WHERE instrument_id = $1::uuid AND price_date = $2`, instID, d(2024, 1, 1)).Scan(&open, &high, &low, &vol)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if open.Valid || high.Valid || low.Valid || vol.Valid {
		t.Error("expected nullable fields to be NULL")
	}
}

func TestUpsertPrices_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	err := p.UpsertPrices(ctx, nil)
	if err != nil {
		t.Fatalf("empty upsert should not error: %v", err)
	}
}

// --- FXGaps tests ---

// setupInstrumentWithCurrency creates an instrument with a specific asset class and currency.
func setupInstrumentWithCurrency(t *testing.T, p *Postgres, desc, assetClass, currency string) string {
	t.Helper()
	ctx := context.Background()
	id, err := p.EnsureInstrument(ctx, assetClass, "", currency, desc, []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "TEST", Value: desc, Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument %s: %v", desc, err)
	}
	return id
}

// lookupFXInstrument finds the FX pair instrument ID for a given currency.
func lookupFXInstrument(t *testing.T, p *Postgres, currency string) string {
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

func TestFXGaps_MixedCurrencies(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// Create instruments: one EUR, one GBP, one USD.
	eurInst := setupInstrumentWithCurrency(t, p, "SAP", "STOCK", "EUR")
	gbpInst := setupInstrumentWithCurrency(t, p, "HSBC", "STOCK", "GBP")
	usdInst := setupInstrumentWithCurrency(t, p, "AAPL-FX", "STOCK", "USD")

	// Buy all three on Jan 10, sell on Feb 10.
	insertTxs(t, p, userID, eurInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "SAP", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "SAP", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 1, 10), InstrumentDescription: "HSBC", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: "A",
	}, gbpInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 2, 10), InstrumentDescription: "HSBC", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: "A",
	}, gbpInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST3", "A", &apiv1.Tx{
		Timestamp: ts(2024, 1, 10), InstrumentDescription: "AAPL-FX", Type: apiv1.TxType_BUYSTOCK, Quantity: 20, Account: "A",
	}, usdInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST3", "A", &apiv1.Tx{
		Timestamp: ts(2024, 2, 10), InstrumentDescription: "AAPL-FX", Type: apiv1.TxType_SELLSTOCK, Quantity: -20, Account: "A",
	}, usdInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// FXGaps should return gaps for EUR/USD and GBP/USD but NOT for USD instruments.
	eurFX := lookupFXInstrument(t, p, "EUR")
	gbpFX := lookupFXInstrument(t, p, "GBP")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 10)},
	})
	assertInstrumentRanges(t, got, gbpFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 10)},
	})
	// USD instrument should NOT produce any FX gaps.
	assertInstrumentRanges(t, got, usdInst, nil)
}

func TestFXGaps_PartialCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	eurInst := setupInstrumentWithCurrency(t, p, "SAP-PC", "STOCK", "EUR")
	insertTxs(t, p, userID, eurInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "SAP-PC", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 1, 20), InstrumentDescription: "SAP-PC", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	eurFX := lookupFXInstrument(t, p, "EUR")

	// Insert some FX rate coverage for Jan 13-15.
	for i := 13; i <= 15; i++ {
		insertPrice(t, p, eurFX, d(2024, 1, i), 1.08)
	}

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// Gaps should be [Jan 10, Jan 13) and [Jan 16, Jan 20).
	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 1, 13)},
		{From: d(2024, 1, 16), To: d(2024, 1, 20)},
	})
}

func TestFXGaps_AllUSD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	usdInst := setupInstrumentWithCurrency(t, p, "AAPL-USD", "STOCK", "USD")
	insertTxs(t, p, userID, usdInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "AAPL-USD", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "AAPL-USD", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no FX gaps for all-USD portfolio, got %d", len(got))
	}
}

func TestFXGaps_MultipleInstrumentsSameCurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// Two EUR instruments with overlapping hold periods.
	eurInst1 := setupInstrumentWithCurrency(t, p, "SAP-M1", "STOCK", "EUR")
	eurInst2 := setupInstrumentWithCurrency(t, p, "BMW-M1", "STOCK", "EUR")

	insertTxs(t, p, userID, eurInst1, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 5), InstrumentDescription: "SAP-M1", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 1, 20), InstrumentDescription: "SAP-M1", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 1, 15), InstrumentDescription: "BMW-M1", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: "A",
	}, eurInst2); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 1, 30), InstrumentDescription: "BMW-M1", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: "A",
	}, eurInst2); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	eurFX := lookupFXInstrument(t, p, "EUR")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// Should produce a single merged range for EUR/USD: [Jan 5, Jan 30).
	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 5), To: d(2024, 1, 30)},
	})
}

func TestFXGaps_DisplayCurrency_USDHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// User holds only USD instruments but sets display=EUR.
	if err := p.SetDisplayCurrency(ctx, userID, "EUR"); err != nil {
		t.Fatalf("set display currency: %v", err)
	}

	usdInst := setupInstrumentWithCurrency(t, p, "AAPL-DC", "STOCK", "USD")
	insertTxs(t, p, userID, usdInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "AAPL-DC", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "AAPL-DC", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	eurFX := lookupFXInstrument(t, p, "EUR")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// Even though all holdings are USD, we need EUR/USD rates because display=EUR.
	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 10)},
	})
}

func TestFXGaps_DisplayCurrency_SkipsSameCurrency(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// User holds only EUR instruments and display=EUR. No EUR/USD rate needed.
	if err := p.SetDisplayCurrency(ctx, userID, "EUR"); err != nil {
		t.Fatalf("set display currency: %v", err)
	}

	eurInst := setupInstrumentWithCurrency(t, p, "SAP-DC", "STOCK", "EUR")
	insertTxs(t, p, userID, eurInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "SAP-DC", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A"},
		{Timestamp: ts(2024, 2, 10), InstrumentDescription: "SAP-DC", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A"},
	})

	eurFX := lookupFXInstrument(t, p, "EUR")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// EUR/USD is still needed from source 1 (held EUR instrument → base currency rate),
	// but the display currency source should NOT add additional ranges since
	// instrument currency == display currency.
	// Source 1 produces [Jan 10, Feb 10) for EUR/USD. No extra from display.
	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 2, 10)},
	})
}

func TestFXGaps_DisplayCurrency_MixedHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// User holds GBP instrument Jan 10-20, USD instrument Feb 1-10, display=EUR.
	if err := p.SetDisplayCurrency(ctx, userID, "EUR"); err != nil {
		t.Fatalf("set display currency: %v", err)
	}

	gbpInst := setupInstrumentWithCurrency(t, p, "HSBC-DC", "STOCK", "GBP")
	usdInst := setupInstrumentWithCurrency(t, p, "AAPL-DC2", "STOCK", "USD")

	insertTxs(t, p, userID, gbpInst, []*apiv1.Tx{
		{Timestamp: ts(2024, 1, 10), InstrumentDescription: "HSBC-DC", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: "A"},
		{Timestamp: ts(2024, 1, 20), InstrumentDescription: "HSBC-DC", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: "A"},
	})
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 2, 1), InstrumentDescription: "AAPL-DC2", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: "A",
	}, usdInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "TEST2", "A", &apiv1.Tx{
		Timestamp: ts(2024, 2, 10), InstrumentDescription: "AAPL-DC2", Type: apiv1.TxType_SELLSTOCK, Quantity: -10, Account: "A",
	}, usdInst); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	eurFX := lookupFXInstrument(t, p, "EUR")
	gbpFX := lookupFXInstrument(t, p, "GBP")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// GBP/USD needed from source 1 (held GBP instrument): [Jan 10, Jan 20).
	assertInstrumentRanges(t, got, gbpFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 1, 20)},
	})

	// EUR/USD needed from source 2 (display=EUR):
	// - GBP instrument [Jan 10, Jan 20) has currency != EUR → need EUR/USD
	// - USD instrument [Feb 1, Feb 10) has currency != EUR (USD, absent from heldToCurrency) → need EUR/USD
	// Merged: [Jan 10, Jan 20) + [Feb 1, Feb 10)
	assertInstrumentRanges(t, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), To: d(2024, 1, 20)},
		{From: d(2024, 2, 1), To: d(2024, 2, 10)},
	})
}

func TestFXGaps_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty results, got %d", len(got))
	}
}
