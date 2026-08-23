package postgres

import (
	"context"
	"database/sql"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
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

// setupInstrument creates an instrument with a broker description identifier and
// a USD line. The currency is not decoration: prices hang off the listing, and a
// listing with no currency is not priceable, so an instrument seeded without one
// has nothing for a price to attach to.
func setupInstrument(t *testing.T, p *Postgres, desc string) string {
	t.Helper()
	ctx := context.Background()
	id, _, err := p.EnsureInstrument(ctx, "", "", "USD", "", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: "TEST"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
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
	// Weightless, so the store routes no counterparty beside these: the fixtures
	// using this are about prices and corporate actions over the postings they
	// name, not about what a group owes. See createTx.
	if err := p.ReplaceTxsInPeriod(ctx, userID, "TEST", "", from, to, txs, ids, weightlessFor(ids), nil); err != nil {
		t.Fatalf("insert txs: %v", err)
	}
}

// pricedListing is the line an instrument's prices hang off in these fixtures:
// its single currency-bearing listing. Returns "" where it has none, which is
// what an assertion of absence needs.
func pricedListing(t testing.TB, p *Postgres, instID string) string {
	t.Helper()
	var id sql.NullString
	err := p.q.QueryRowContext(context.Background(), `
		SELECT listing_id::text FROM instrument_priced_listing WHERE instrument_id = $1::uuid
	`, instID).Scan(&id)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("priced listing for %s: %v", instID, err)
	}
	return id.String
}

// insertPrice inserts a single eod_prices row and the coverage that goes with
// it, both against the instrument's priced line. Fixtures record both so they
// cannot construct a state the write path never produces: a bar outside any
// covered span.
func insertPrice(t *testing.T, p *Postgres, instID string, priceDate time.Time, close float64) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (listing_id, price_date, close, data_provider)
		VALUES ($1::uuid, $2, $3, 'test')
	`, pricedListing(t, p, instID), priceDate, close)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
	insertCoverage(t, p, instID, priceDate, priceDate.Add(db.Day))
}

// insertCoverage records a covered span without any bars in it.
func insertCoverage(t *testing.T, p *Postgres, instID string, from, before time.Time) {
	t.Helper()
	listingID := pricedListing(t, p, instID)
	err := p.runInTx(context.Background(), func(exec queryable) error {
		return upsertCoverageSpan(context.Background(), exec, priceCoverage, listingID, "test", from, before, nil)
	})
	if err != nil {
		t.Fatalf("insert coverage: %v", err)
	}
}

// assertInstrumentRanges checks the ranges reported for an instrument's priced
// line. The tests are written in terms of securities because that is what their
// transactions name; the results are per listing, and this is where the two meet.
func assertInstrumentRanges(t *testing.T, p *Postgres, got []db.ListingDateRanges, instID string, want []db.DateRange) {
	t.Helper()
	listingID := pricedListing(t, p, instID)
	var found *db.ListingDateRanges
	for i := range got {
		if listingID != "" && got[i].ListingID == listingID {
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
		if !found.Ranges[i].From.Equal(want[i].From) || !found.Ranges[i].Before.Equal(want[i].Before) {
			t.Errorf("instrument %s range[%d]: got [%s, %s), want [%s, %s)",
				instID, i,
				found.Ranges[i].From.Format("2006-01-02"), found.Ranges[i].Before.Format("2006-01-02"),
				want[i].From.Format("2006-01-02"), want[i].Before.Format("2006-01-02"))
		}
	}
}

func fmtRanges(rs []db.DateRange) string {
	s := "["
	for i, r := range rs {
		if i > 0 {
			s += ", "
		}
		s += "[" + r.From.Format("2006-01-02") + ", " + r.Before.Format("2006-01-02") + ")"
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "A"},
		{OrderDate: ts(2024, 3, 15),
			TradeDate: ts(2024, 3, 15), InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-100", Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 3, 15)},
	})
}

func TestHeldRanges_OpenPosition(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GOOG")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 6, 1),
			TradeDate: ts(2024, 6, 1), InstrumentDescription: "GOOG", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "A"},
	})

	today := time.Now().UTC().Truncate(db.Day)

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{ExtendToToday: true})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 6, 1), Before: today.Add(db.Day)},
	})
}

func TestHeldRanges_OpenPositionNoExtend(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "MSFT")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 6, 1),
			TradeDate: ts(2024, 6, 1), InstrumentDescription: "MSFT", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{ExtendToToday: false})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	// Without extend, open position just gets +1 day from range start.
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 6, 1), Before: d(2024, 6, 2)},
	})
}

func TestHeldRanges_CloseAndReopen(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "TSLA")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "TSLA", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "A"},
		{OrderDate: ts(2024, 2, 15),
			TradeDate: ts(2024, 2, 15), InstrumentDescription: "TSLA", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-100", Account: "A"},
		{OrderDate: ts(2024, 4, 1),
			TradeDate: ts(2024, 4, 1), InstrumentDescription: "TSLA", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "50", Account: "A"},
		{OrderDate: ts(2024, 5, 1),
			TradeDate: ts(2024, 5, 1), InstrumentDescription: "TSLA", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-50", Account: "A"},
	})

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 15)},
		{From: d(2024, 4, 1), Before: d(2024, 5, 1)},
	})
}

func TestHeldRanges_UnidentifiedExcluded(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	// Insert a tx with NULL instrument_id directly via SQL.
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
		                 broker_tx_type, resolved_tx_type, quantity, weight, weight_commodity, group_id)
		VALUES ($1::uuid, 'TEST', 'A', $2, $2, 'UNKNOWN', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 100, 100, 'desc:UNKNOWN', $3::uuid)
	`, userID, d(2024, 6, 1), newTxGroup(t, p, userID))
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
		{OrderDate: ts(2024, 1, 1),
			TradeDate: ts(2024, 1, 1), InstrumentDescription: "INST1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 1),
			TradeDate: ts(2024, 2, 1), InstrumentDescription: "INST1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})

	// Insert txs for inst2 using CreateTx to avoid ReplaceTxsInPeriod conflict with same broker/period.
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 3, 1),
		TradeDate: ts(2024, 3, 1), InstrumentDescription: "INST2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "20", Account: "A",
	}, inst2, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 4, 1),
		TradeDate: ts(2024, 4, 1), InstrumentDescription: "INST2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-20", Account: "A",
	}, inst2, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 instruments, got %d", len(got))
	}
	assertInstrumentRanges(t, p, got, inst1, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 2, 1)},
	})
	assertInstrumentRanges(t, p, got, inst2, []db.DateRange{
		{From: d(2024, 3, 1), Before: d(2024, 4, 1)},
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
		{OrderDate: ts(2024, 1, 1),
			TradeDate: ts(2024, 1, 1), InstrumentDescription: "SHARED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 1),
			TradeDate: ts(2024, 2, 1), InstrumentDescription: "SHARED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})

	// User 2 holds Mar-Apr (separate broker to avoid replace conflict).
	if err := createTx(ctx, p, user2, "TEST2", "B", "", &apiv1.Tx{
		OrderDate: ts(2024, 3, 1),
		TradeDate: ts(2024, 3, 1), InstrumentDescription: "SHARED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "B",
	}, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, user2, "TEST2", "B", "", &apiv1.Tx{
		OrderDate: ts(2024, 4, 1),
		TradeDate: ts(2024, 4, 1), InstrumentDescription: "SHARED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-5", Account: "B",
	}, instID, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// System-wide: should see both ranges.
	got, err := p.HeldRanges(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("held ranges: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 2, 1)},
		{From: d(2024, 3, 1), Before: d(2024, 4, 1)},
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
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 6)},
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
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 4)},
		{From: d(2024, 1, 10), Before: d(2024, 1, 13)},
	})
}

func TestPriceCoverage_WeekendGapNotBridged(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "COV3")

	// Fri Jan 5, Mon Jan 8 (weekend gap = 2 calendar days).
	// Without BridgeRanges, these are two separate ranges.
	// The price worker fills weekends with synthetic prices so coverage
	// is contiguous in practice, but PriceCoverage reports raw ranges.
	insertPrice(t, p, instID, d(2024, 1, 5), 100.0)
	insertPrice(t, p, instID, d(2024, 1, 8), 100.0)

	got, err := p.PriceCoverage(ctx, nil)
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 5), Before: d(2024, 1, 6)},
		{From: d(2024, 1, 8), Before: d(2024, 1, 9)},
	})
}

func TestPriceCoverage_FilterByListing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst1 := setupInstrument(t, p, "FILT1")
	inst2 := setupInstrument(t, p, "FILT2")

	insertPrice(t, p, inst1, d(2024, 1, 1), 100.0)
	insertPrice(t, p, inst2, d(2024, 2, 1), 200.0)

	// Filter to inst1's line only.
	got, err := p.PriceCoverage(ctx, []string{pricedListing(t, p, inst1)})
	if err != nil {
		t.Fatalf("price coverage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 listing, got %d", len(got))
	}
	assertInstrumentRanges(t, p, got, inst1, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 2)},
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
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 6, 15), Before: d(2024, 6, 16)},
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "GAPNONE", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "100", Account: "A"},
		{OrderDate: ts(2024, 2, 10),
			TradeDate: ts(2024, 2, 10), InstrumentDescription: "GAPNONE", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-100", Account: "A"},
	})

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	// With no prices, gaps = entire held range.
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 10)},
	})
}

func TestPriceGaps_FullCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPFULL")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 1),
			TradeDate: ts(2024, 1, 1), InstrumentDescription: "GAPFULL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 1, 4),
			TradeDate: ts(2024, 1, 4), InstrumentDescription: "GAPFULL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
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
	assertInstrumentRanges(t, p, got, instID, nil)
}

func TestPriceGaps_PartialCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPPART")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 1),
			TradeDate: ts(2024, 1, 1), InstrumentDescription: "GAPPART", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "GAPPART", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})

	// Prices for Jan 3-5 only (gap before and after).
	for i := 2; i < 5; i++ {
		insertPrice(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), 100.0)
	}

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	assertInstrumentRanges(t, p, got, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 3)},
		{From: d(2024, 1, 6), Before: d(2024, 1, 10)},
	})
}

// A range a provider answered with nothing is not a gap. This is the case the
// old row-presence inference could not express, and the reason the fetcher used
// to re-ask about delisted and pre-IPO ranges on every cycle.
func TestPriceGaps_EmptyCoverageIsNotAGap(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)
	instID := setupInstrument(t, p, "GAPEMPTY")

	insertTxs(t, p, userID, instID, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 1),
			TradeDate: ts(2024, 1, 1), InstrumentDescription: "GAPEMPTY", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "GAPEMPTY", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})

	// The whole held period was asked about and came back with no bars at all.
	insertCoverage(t, p, instID, d(2024, 1, 1), d(2024, 1, 10))

	got, err := p.PriceGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("price gaps: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no gaps for a fully covered period, got %+v", got)
	}
}

// Coverage is kept per plugin so that what one plugin settled does not hide the
// range from another that has never been asked.
func TestPriceCoverageByPlugin_SeparatesPlugins(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "PERPLUGIN")

	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", nil, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert massive: %v", err)
	}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "eodhd", nil, d(2024, 2, 1), d(2024, 2, 11), nil); err != nil {
		t.Fatalf("upsert eodhd: %v", err)
	}

	got, err := p.PriceCoverageByPlugin(ctx, []string{pricedListing(t, p, instID)})
	if err != nil {
		t.Fatalf("coverage by plugin: %v", err)
	}
	assertRanges(t, got[pricedListing(t, p, instID)]["massive"], []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 11)}})
	assertRanges(t, got[pricedListing(t, p, instID)]["eodhd"], []db.DateRange{{From: d(2024, 2, 1), Before: d(2024, 2, 11)}})

	// Merged across plugins, both spans show up for the instrument.
	merged, err := p.PriceCoverage(ctx, []string{pricedListing(t, p, instID)})
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	assertInstrumentRanges(t, p, merged, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 11)},
		{From: d(2024, 2, 1), Before: d(2024, 2, 11)},
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

	open := decf(100)
	high := decf(105)
	low := decf(99)
	vol := int64(1000)
	err := p.UpsertPrices(ctx, []db.EODPrice{
		{
			ListingID:    pricedListing(t, p, instID),
			PriceDate:    d(2024, 1, 1),
			Open:         &open,
			High:         &high,
			Low:          &low,
			Close:        decf(102.0),
			Volume:       &vol,
			DataProvider: "test",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify via coverage.
	cov, err := p.PriceCoverage(ctx, []string{pricedListing(t, p, instID)})
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	assertInstrumentRanges(t, p, cov, instID, []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 2)},
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
			ListingID:    pricedListing(t, p, instID),
			PriceDate:    d(2024, 1, 1),
			Close:        decf(200.0),
			DataProvider: "updated",
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify updated value.
	var close float64
	var provider string
	err = p.q.QueryRowContext(ctx, `SELECT close, data_provider FROM eod_prices WHERE listing_id = $1::uuid AND price_date = $2`, pricedListing(t, p, instID), d(2024, 1, 1)).Scan(&close, &provider)
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
			ListingID:    pricedListing(t, p, instID),
			PriceDate:    d(2024, 1, 1),
			Close:        decf(50.0),
			DataProvider: "test",
			// Open, High, Low, Volume all nil
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var open, high, low sql.NullFloat64
	var vol sql.NullInt64
	err = p.q.QueryRowContext(ctx, `SELECT open, high, low, volume FROM eod_prices WHERE listing_id = $1::uuid AND price_date = $2`, pricedListing(t, p, instID), d(2024, 1, 1)).Scan(&open, &high, &low, &vol)
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

// Non-trading days get no row. The filled series is derived at read time from
// (bars, coverage), so storing it would be a second copy of a derivable fact.
func TestUpsertPricesForRange_StoresOnlyRealBars(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "FILL1")

	// Mon-Fri real bars over a Mon-Mon range, so Sat and Sun have no bar.
	mon := d(2024, 1, 1)
	var bars []db.EODPrice
	for i := 0; i < 5; i++ {
		bars = append(bars, db.EODPrice{
			ListingID: pricedListing(t, p, instID), PriceDate: mon.AddDate(0, 0, i), Close: decf(102).Add(decimal.NewFromInt(int64(i))),
		})
	}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", bars, mon, mon.AddDate(0, 0, 7), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	var count int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM eod_prices WHERE listing_id = $1::uuid`, pricedListing(t, p, instID)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 stored bars and no filled rows, got %d", count)
	}
	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: mon, Before: mon.AddDate(0, 0, 7)},
	})
	assertCoverageContains(t, p)
}

// The declared range is coverage even where it holds no bars, so a range asked
// about and answered with nothing is not mistaken for one never asked about.
func TestUpsertPricesForRange_NoBarsStillCovers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "FILL3")

	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", nil, d(2024, 1, 1), d(2024, 1, 4), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	var count int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM eod_prices WHERE listing_id = $1::uuid`, pricedListing(t, p, instID)).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no price rows, got %d", count)
	}
	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 4)},
	})
}

// Providers can repeat a date within one response; the last one supplied wins.
func TestUpsertPricesForRange_DuplicateDates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "FILL5")

	day := d(2024, 1, 2)
	bars := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: day, Close: decf(100.0)},
		{ListingID: pricedListing(t, p, instID), PriceDate: day, Close: decf(101.0)},
	}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", bars, d(2024, 1, 1), d(2024, 1, 4), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	var close float64
	if err := p.q.QueryRowContext(ctx,
		`SELECT close FROM eod_prices WHERE listing_id = $1::uuid AND price_date = $2`,
		pricedListing(t, p, instID), day).Scan(&close); err != nil {
		t.Fatalf("query: %v", err)
	}
	if close != 101.0 {
		t.Errorf("close = %v, want 101.0 (last supplied wins)", close)
	}
}

// setupInstrumentWithCurrency creates an instrument with a specific asset class and currency.
func setupInstrumentWithCurrency(t *testing.T, p *Postgres, desc, assetClass, currency string) string {
	t.Helper()
	ctx := context.Background()
	id, _, err := p.EnsureInstrument(ctx, assetClass, "", currency, desc, "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: desc, Domain: "TEST"},
			Canonical: false,
		}}, nil, "", nil, nil, nil)
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "SAP", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 10),
			TradeDate: ts(2024, 2, 10), InstrumentDescription: "SAP", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 1, 10),
		TradeDate: ts(2024, 1, 10), InstrumentDescription: "HSBC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "A",
	}, gbpInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 2, 10),
		TradeDate: ts(2024, 2, 10), InstrumentDescription: "HSBC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-5", Account: "A",
	}, gbpInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST3", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 1, 10),
		TradeDate: ts(2024, 1, 10), InstrumentDescription: "AAPL-FX", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "20", Account: "A",
	}, usdInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST3", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 2, 10),
		TradeDate: ts(2024, 2, 10), InstrumentDescription: "AAPL-FX", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-20", Account: "A",
	}, usdInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	// FXGaps should return gaps for EUR/USD and GBP/USD but NOT for USD instruments.
	eurFX := lookupFXInstrument(t, p, "EUR")
	gbpFX := lookupFXInstrument(t, p, "GBP")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 10)},
	})
	assertInstrumentRanges(t, p, got, gbpFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 10)},
	})
	// USD instrument should NOT produce any FX gaps.
	assertInstrumentRanges(t, p, got, usdInst, nil)
}

func TestFXGaps_PartialCoverage(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	eurInst := setupInstrumentWithCurrency(t, p, "SAP-PC", "STOCK", "EUR")
	insertTxs(t, p, userID, eurInst, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "SAP-PC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 1, 20),
			TradeDate: ts(2024, 1, 20), InstrumentDescription: "SAP-PC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
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
	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 1, 13)},
		{From: d(2024, 1, 16), Before: d(2024, 1, 20)},
	})
}

func TestFXGaps_AllUSD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := setupUser(t, p)

	usdInst := setupInstrumentWithCurrency(t, p, "AAPL-USD", "STOCK", "USD")
	insertTxs(t, p, userID, usdInst, []*apiv1.Tx{
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "AAPL-USD", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 10),
			TradeDate: ts(2024, 2, 10), InstrumentDescription: "AAPL-USD", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
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
		{OrderDate: ts(2024, 1, 5),
			TradeDate: ts(2024, 1, 5), InstrumentDescription: "SAP-M1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 1, 20),
			TradeDate: ts(2024, 1, 20), InstrumentDescription: "SAP-M1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 1, 15),
		TradeDate: ts(2024, 1, 15), InstrumentDescription: "BMW-M1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "A",
	}, eurInst2, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 1, 30),
		TradeDate: ts(2024, 1, 30), InstrumentDescription: "BMW-M1", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-5", Account: "A",
	}, eurInst2, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	eurFX := lookupFXInstrument(t, p, "EUR")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// Should produce a single merged range for EUR/USD: [Jan 5, Jan 30).
	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 5), Before: d(2024, 1, 30)},
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "AAPL-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 10),
			TradeDate: ts(2024, 2, 10), InstrumentDescription: "AAPL-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
	})

	eurFX := lookupFXInstrument(t, p, "EUR")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// Even though all holdings are USD, we need EUR/USD rates because display=EUR.
	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 10)},
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "SAP-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A"},
		{OrderDate: ts(2024, 2, 10),
			TradeDate: ts(2024, 2, 10), InstrumentDescription: "SAP-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A"},
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
	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 2, 10)},
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
		{OrderDate: ts(2024, 1, 10),
			TradeDate: ts(2024, 1, 10), InstrumentDescription: "HSBC-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "5", Account: "A"},
		{OrderDate: ts(2024, 1, 20),
			TradeDate: ts(2024, 1, 20), InstrumentDescription: "HSBC-DC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-5", Account: "A"},
	})
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 2, 1),
		TradeDate: ts(2024, 2, 1), InstrumentDescription: "AAPL-DC2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "10", Account: "A",
	}, usdInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}
	if err := createTx(ctx, p, userID, "TEST2", "A", "", &apiv1.Tx{
		OrderDate: ts(2024, 2, 10),
		TradeDate: ts(2024, 2, 10), InstrumentDescription: "AAPL-DC2", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, ResolvedTxType: typev1.TxType_TRADE_ASSET, Quantity: "-10", Account: "A",
	}, usdInst, nil); err != nil {
		t.Fatalf("create tx: %v", err)
	}

	eurFX := lookupFXInstrument(t, p, "EUR")
	gbpFX := lookupFXInstrument(t, p, "GBP")

	got, err := p.FXGaps(ctx, db.HeldRangesOpts{})
	if err != nil {
		t.Fatalf("FXGaps: %v", err)
	}

	// GBP/USD needed from source 1 (held GBP instrument): [Jan 10, Jan 20).
	assertInstrumentRanges(t, p, got, gbpFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 1, 20)},
	})

	// EUR/USD needed from source 2 (display=EUR):
	// - GBP instrument [Jan 10, Jan 20) has currency != EUR → need EUR/USD
	// - USD instrument [Feb 1, Feb 10) has currency != EUR (USD, absent from heldToCurrency) → need EUR/USD
	// Merged: [Jan 10, Jan 20) + [Feb 1, Feb 10)
	assertInstrumentRanges(t, p, got, eurFX, []db.DateRange{
		{From: d(2024, 1, 10), Before: d(2024, 1, 20)},
		{From: d(2024, 2, 1), Before: d(2024, 2, 10)},
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
