package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// insertPriceWithProvider inserts a single eod_prices row with a specific provider.
func insertPriceWithProvider(t *testing.T, p *Postgres, instID string, priceDate time.Time, close float64, provider string) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (listing_id, price_date, close, data_provider)
		VALUES ($1::uuid, $2, $3, $4)
	`, pricedListing(t, p, instID), priceDate, close, provider)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
	insertCoverage(t, p, instID, priceDate, priceDate.Add(db.Day))
}

// insertPriceFull inserts a price row with all OHLCV fields.
func insertPriceFull(t *testing.T, p *Postgres, instID string, priceDate time.Time, open, high, low, close float64, volume int64, provider string) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (listing_id, price_date, open, high, low, close, volume, data_provider)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
	`, pricedListing(t, p, instID), priceDate, open, high, low, close, volume, provider)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
	insertCoverage(t, p, instID, priceDate, priceDate.Add(db.Day))
}

func TestListPrices_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	rows, total, nextToken, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 0 || len(rows) != 0 || nextToken != "" {
		t.Fatalf("expected empty, got total=%d rows=%d token=%q", total, len(rows), nextToken)
	}
}

func TestListPrices_Basic(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	insertPriceFull(t, p, instID, d(2024, 1, 15), 100, 105, 99, 102, 1000, "massive")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 16), 103, "massive")

	rows, total, _, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Should be ordered by date DESC.
	if rows[0].PriceDate.After(rows[1].PriceDate) == false {
		// First row should be later date.
		if !rows[0].PriceDate.Equal(d(2024, 1, 16)) {
			t.Errorf("expected first row date 2024-01-16, got %s", rows[0].PriceDate.Format("2006-01-02"))
		}
	}
	// Check display name resolved.
	if rows[0].InstrumentDisplayName == "" {
		t.Error("expected non-empty display name")
	}
	// Check OHLCV on the full row.
	fullRow := rows[1] // 2024-01-15
	if fullRow.Open == nil || fullRow.Open.String() != "100" {
		t.Errorf("expected open=100, got %v", fullRow.Open)
	}
	if fullRow.Volume == nil || *fullRow.Volume != 1000 {
		t.Errorf("expected volume=1000, got %v", fullRow.Volume)
	}
}

func TestListPrices_Search(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst1 := setupInstrument(t, p, "AAPL")
	inst2 := setupInstrument(t, p, "GOOG")

	insertPriceWithProvider(t, p, inst1, d(2024, 1, 15), 100, "test")
	insertPriceWithProvider(t, p, inst2, d(2024, 1, 15), 200, "test")

	rows, total, _, err := p.ListPrices(ctx, "AAPL", time.Time{}, time.Time{}, "", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].InstrumentID != inst1 {
		t.Errorf("expected instrument %s, got %s", inst1, rows[0].InstrumentID)
	}
}

func TestListPrices_DateRange(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "MSFT")

	insertPriceWithProvider(t, p, instID, d(2024, 1, 10), 100, "test")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 20), 110, "test")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 30), 120, "test")

	rows, total, _, err := p.ListPrices(ctx, "", d(2024, 1, 15), d(2024, 1, 25), "", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(rows) != 1 || !rows[0].PriceDate.Equal(d(2024, 1, 20)) {
		t.Fatalf("expected row for 2024-01-20, got %v", rows)
	}
}

func TestListPrices_DateRangeExcludesBefore(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "MSFT")

	insertPriceWithProvider(t, p, instID, d(2024, 1, 19), 100, "test")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 20), 110, "test")

	// The upper bound is exclusive: a row on it is out, the day before is in.
	rows, total, _, err := p.ListPrices(ctx, "", d(2024, 1, 1), d(2024, 1, 20), "", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(rows) != 1 || !rows[0].PriceDate.Equal(d(2024, 1, 19)) {
		t.Fatalf("expected row for 2024-01-19, got %v", rows)
	}
}

func TestListPrices_DataProviderFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "TSLA")

	insertPriceWithProvider(t, p, instID, d(2024, 1, 15), 100, "massive")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 16), 110, "yahoo")

	rows, total, _, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "massive", 30, "")
	if err != nil {
		t.Fatalf("list prices: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(rows) != 1 || rows[0].DataProvider != "massive" {
		t.Fatalf("expected massive provider, got %v", rows)
	}
}

func TestListPricesForExport_IdentifierPrecedence(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create instrument with both ISIN (priority 3) and MIC_TICKER (priority 1).
	instID, _, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "Apple", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		},
		{
			Ref:       db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}

	insertPriceWithProvider(t, p, instID, d(2024, 1, 15), 185.90, "test")

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// MIC_TICKER should win over ISIN (most plugin-compatible).
	if rows[0].Ref.Type != "MIC_TICKER" {
		t.Errorf("expected MIC_TICKER, got %s", rows[0].Ref.Type)
	}
	if rows[0].Ref.Value != "AAPL" {
		t.Errorf("expected AAPL, got %s", rows[0].Ref.Value)
	}
	if rows[0].AssetClass != "STOCK" {
		t.Errorf("expected asset_class=STOCK, got %s", rows[0].AssetClass)
	}
	if rows[0].Close.String() != "185.9" {
		t.Errorf("expected close=185.9, got %v", rows[0].Close)
	}
}

func TestListPricesForExport_NoIdentifiersExcluded(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	// Create instrument with identifier, then insert prices for both.
	instWithID := setupInstrument(t, p, "AAPL")
	insertPriceWithProvider(t, p, instWithID, d(2024, 1, 15), 100, "test")

	// Create instrument without any identifiers by inserting directly, minting its
	// listing alongside: every security has at least one currency line. The line
	// carries a currency because the prices below have to hang off something
	// priceable; what this test is about is the missing identifier.
	var instNoID string
	err := p.q.QueryRowContext(ctx, `
		WITH ins AS (
			INSERT INTO instruments DEFAULT VALUES RETURNING id
		), lst AS (
			INSERT INTO instrument_listings (instrument_id, currency) SELECT id, 'USD' FROM ins
		)
		SELECT id FROM ins
	`).Scan(&instNoID)
	if err != nil {
		t.Fatalf("insert bare instrument: %v", err)
	}
	insertPriceWithProvider(t, p, instNoID, d(2024, 1, 15), 200, "test")

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	// Only the instrument with identifiers should appear.
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (no-identifier excluded), got %d", len(rows))
	}
	if rows[0].Close.String() != "100" {
		t.Errorf("expected close=100, got %v", rows[0].Close)
	}
}

func TestListPricesForExport_OHLCVFields(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupInstrument(t, p, "MSFT")
	insertPriceFull(t, p, instID, d(2024, 1, 15), 100, 105, 99, 102, 50000, "test")

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Open == nil || r.Open.String() != "100" {
		t.Errorf("expected open=100, got %v", r.Open)
	}
	if r.High == nil || r.High.String() != "105" {
		t.Errorf("expected high=105, got %v", r.High)
	}
	if r.Low == nil || r.Low.String() != "99" {
		t.Errorf("expected low=99, got %v", r.Low)
	}
	if r.Close.String() != "102" {
		t.Errorf("expected close=102, got %v", r.Close)
	}
	if r.Volume == nil || *r.Volume != 50000 {
		t.Errorf("expected volume=50000, got %v", r.Volume)
	}
}

// A basis equal to the bar's own date is the as-traded convention the column
// defaults to, and the export reports it as absent so a file does not restate
// it on every row. Only a restated bar carries a value.
func TestListPricesForExport_ShareCountBasis(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupTickerInstrument(t, p, "NVDA")
	basis := d(2024, 6, 10)
	if err := p.UpsertPrices(ctx, []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 15), Close: decf(48), DataProvider: "test"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 16), Close: decf(4), DataProvider: "test", ShareCountBasis: &basis},
	}); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].ShareCountBasis != nil {
		t.Errorf("expected as-traded bar to report no basis, got %v", rows[0].ShareCountBasis)
	}
	if rows[1].ShareCountBasis == nil || !rows[1].ShareCountBasis.Equal(basis) {
		t.Errorf("expected basis 2024-06-10, got %v", rows[1].ShareCountBasis)
	}
}

// Two venues can list the same ticker, so the identifier value alone does not
// name an instrument. A consumer that breaks groups on a key change needs the
// rows for one instrument to arrive together, which they do not if the domain
// is left out of the ordering.
func TestListPricesForExport_OrdersByDomain(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	xlon, _, err := p.EnsureInstrument(ctx, "STOCK", "XLON", "GBP", "VOD", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "VOD", Domain: "XLON"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XLON instrument: %v", err)
	}
	xnas, _, err := p.EnsureInstrument(ctx, "STOCK", "XNAS", "USD", "VOD", "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "VOD", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure XNAS instrument: %v", err)
	}

	// Interleaved by date, so an ordering that ignores the domain interleaves
	// the output too.
	insertPriceWithProvider(t, p, xlon, d(2024, 1, 15), 70, "test")
	insertPriceWithProvider(t, p, xnas, d(2024, 1, 16), 9, "test")
	insertPriceWithProvider(t, p, xlon, d(2024, 1, 17), 71, "test")

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Ref.Domain
	}
	want := []string{"XLON", "XLON", "XNAS"}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected domains %v, got %v", want, got)
		}
	}
}

func TestListPricesForExport_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestListPrices_Pagination(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "NVDA")

	for i := 0; i < 5; i++ {
		insertPriceWithProvider(t, p, instID, d(2024, 1, 1).AddDate(0, 0, i), float64(100+i), "test")
	}

	// Page 1: size 2.
	rows, total, nextToken, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 2, "")
	if err != nil {
		t.Fatalf("list prices page 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total=5, got %d", total)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if nextToken == "" {
		t.Fatal("expected next page token")
	}

	// Page 2.
	rows2, _, nextToken2, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 2, nextToken)
	if err != nil {
		t.Fatalf("list prices page 2: %v", err)
	}
	if len(rows2) != 2 {
		t.Fatalf("expected 2 rows on page 2, got %d", len(rows2))
	}
	if nextToken2 == "" {
		t.Fatal("expected next page token on page 2")
	}

	// Page 3 (last page).
	rows3, _, nextToken3, err := p.ListPrices(ctx, "", time.Time{}, time.Time{}, "", 2, nextToken2)
	if err != nil {
		t.Fatalf("list prices page 3: %v", err)
	}
	if len(rows3) != 1 {
		t.Fatalf("expected 1 row on page 3, got %d", len(rows3))
	}
	if nextToken3 != "" {
		t.Fatalf("expected no next token on last page, got %q", nextToken3)
	}
}

// setupTickerInstrument creates an instrument identified by MIC_TICKER, which
// the export's identifier precedence prefers.
func setupTickerInstrument(t *testing.T, p *Postgres, ticker string) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "STOCK", "XNAS", "USD", ticker, "", "", []db.IdentifierInput{
		{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: ticker, Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument %s: %v", ticker, err)
	}
	return id
}

// The exported span is the declared range, not the dates that happen to have
// rows: the days between the bars are covered and must travel with the file.
func TestListPriceCoverageForExport_SpansDeclaredRange(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupTickerInstrument(t, p, "AAPL")
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "test", []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 15), Close: decf(100)},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 18), Close: decf(110)},
	}, d(2024, 1, 15), d(2024, 1, 19), nil); err != nil {
		t.Fatalf("upsert prices for range: %v", err)
	}

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected the 2 real rows, got %d", len(rows))
	}

	cov, err := p.ListPriceCoverageForExport(ctx)
	if err != nil {
		t.Fatalf("list price coverage for export: %v", err)
	}
	if len(cov) != 1 {
		t.Fatalf("expected 1 merged span, got %d", len(cov))
	}
	if !cov[0].From.Equal(d(2024, 1, 15)) || !cov[0].Before.Equal(d(2024, 1, 19)) {
		t.Errorf("expected [2024-01-15, 2024-01-19), got [%s, %s)",
			cov[0].From.Format("2006-01-02"), cov[0].Before.Format("2006-01-02"))
	}
	if cov[0].Ref.Type != "MIC_TICKER" || cov[0].Ref.Value != "AAPL" {
		t.Errorf("got identifier %s %s", cov[0].Ref.Type, cov[0].Ref.Value)
	}
}

func TestListPriceCoverageForExport_SplitsOnGaps(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupTickerInstrument(t, p, "AAPL")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 15), 100, "test")
	insertPriceWithProvider(t, p, instID, d(2024, 1, 16), 101, "test")
	insertPriceWithProvider(t, p, instID, d(2024, 2, 1), 110, "test")

	cov, err := p.ListPriceCoverageForExport(ctx)
	if err != nil {
		t.Fatalf("list price coverage for export: %v", err)
	}
	if len(cov) != 2 {
		t.Fatalf("expected 2 spans either side of the gap, got %d", len(cov))
	}
	if !cov[0].From.Equal(d(2024, 1, 15)) || !cov[0].Before.Equal(d(2024, 1, 17)) {
		t.Errorf("first span [%s, %s)", cov[0].From.Format("2006-01-02"), cov[0].Before.Format("2006-01-02"))
	}
	if !cov[1].From.Equal(d(2024, 2, 1)) || !cov[1].Before.Equal(d(2024, 2, 2)) {
		t.Errorf("second span [%s, %s)", cov[1].From.Format("2006-01-02"), cov[1].Before.Format("2006-01-02"))
	}
}

// An instrument can be covered and hold no bars, and then the coverage query is
// the only place an export can learn its asset class and currency -- which is
// what routes the identifier plugins on the importing side.
func TestListPriceCoverageForExport_CarriesInstrumentContext(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	instID := setupTickerInstrument(t, p, "AAPL")
	insertCoverage(t, p, instID, d(2024, 1, 15), d(2024, 1, 19))

	rows, err := p.ListPricesForExport(ctx)
	if err != nil {
		t.Fatalf("list prices for export: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}

	cov, err := p.ListPriceCoverageForExport(ctx)
	if err != nil {
		t.Fatalf("list price coverage for export: %v", err)
	}
	if len(cov) != 1 {
		t.Fatalf("expected 1 span, got %d", len(cov))
	}
	if cov[0].AssetClass != "STOCK" {
		t.Errorf("expected asset_class=STOCK, got %q", cov[0].AssetClass)
	}
	if cov[0].Currency != "USD" {
		t.Errorf("expected currency=USD, got %q", cov[0].Currency)
	}
}

func TestListPriceCoverageForExport_Empty(t *testing.T) {
	p := testDBTx(t)
	cov, err := p.ListPriceCoverageForExport(context.Background())
	if err != nil {
		t.Fatalf("list price coverage for export: %v", err)
	}
	if len(cov) != 0 {
		t.Fatalf("expected no spans, got %d", len(cov))
	}
}
