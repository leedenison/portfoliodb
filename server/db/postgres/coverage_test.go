package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// readPriceCoverage reads price_coverage directly for an instrument's priced
// line. The DB-layer reader arrives with the gap-analysis change; these tests
// assert the write path in isolation.
func readPriceCoverage(t *testing.T, p *Postgres, instID string) []db.DateRange {
	t.Helper()
	return readListingCoverage(t, p, pricedListing(t, p, instID))
}

// readListingCoverage is the same read for a caller that already holds the line,
// or holds one whose security has since been deleted.
func readListingCoverage(t *testing.T, p *Postgres, listingID string) []db.DateRange {
	t.Helper()
	rows, err := p.q.QueryContext(context.Background(), `
		SELECT covered_from, covered_before FROM price_coverage
		WHERE listing_id = $1::uuid ORDER BY covered_from
	`, listingID)
	if err != nil {
		t.Fatalf("read price coverage: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.DateRange
	for rows.Next() {
		var r db.DateRange
		if err := rows.Scan(&r.From, &r.Before); err != nil {
			t.Fatalf("scan coverage: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate coverage: %v", err)
	}
	return out
}

func assertRanges(t *testing.T, got []db.DateRange, want []db.DateRange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d spans, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if !got[i].From.Equal(want[i].From) || !got[i].Before.Equal(want[i].Before) {
			t.Errorf("span %d: got [%s, %s), want [%s, %s)", i,
				got[i].From.Format("2006-01-02"), got[i].Before.Format("2006-01-02"),
				want[i].From.Format("2006-01-02"), want[i].Before.Format("2006-01-02"))
		}
	}
}

// assertCoverageContains is the containment invariant: every eod_prices row lies
// within some price_coverage span for its listing. Both are keyed on the line,
// so the invariant is stated at that grain and a bar can no longer be excused by
// a span belonging to a sibling line. The converse deliberately does not hold --
// a covered span with no rows is how "we asked and there is nothing there" is
// recorded.
func assertCoverageContains(t *testing.T, p *Postgres) {
	t.Helper()
	rows, err := p.q.QueryContext(context.Background(), `
		SELECT p.listing_id, p.price_date FROM eod_prices p
		WHERE NOT EXISTS (
			SELECT 1 FROM price_coverage c
			WHERE c.listing_id = p.listing_id
			  AND p.price_date >= c.covered_from AND p.price_date < c.covered_before)
	`)
	if err != nil {
		t.Fatalf("check containment: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var listingID string
		var date time.Time
		if err := rows.Scan(&listingID, &date); err != nil {
			t.Fatalf("scan uncovered: %v", err)
		}
		t.Errorf("price row outside coverage: listing %s on %s", listingID, date.Format("2006-01-02"))
	}
}

// UpsertPricesForRange records its declared range, not merely the days it wrote
// rows for: the range is what the provider was asked about.
func TestUpsertPricesForRange_RecordsDeclaredRange(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	bars := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 3), Close: decf(10)},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 5), Close: decf(12)},
	}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", bars, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 11)},
	})
	assertCoverageContains(t, p)
}

// A provider that returns nothing has still covered the range. This is the case
// row presence alone cannot express, and the reason the table exists.
func TestUpsertPricesForRange_EmptyResultStillCovers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "DELISTED")

	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", nil, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert for range: %v", err)
	}

	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: d(2024, 1, 1), Before: d(2024, 1, 11)},
	})
}

// A caller with no range to declare asserts the days it names and nothing more,
// so a gap between supplied bars stays a gap rather than being spanned.
func TestUpsertPrices_CoversSuppliedDatesOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	// Jan 3, 4, 5 are contiguous and merge; Feb 1 stands alone.
	prices := []db.EODPrice{
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 3), Close: decf(10), DataProvider: "import"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 4), Close: decf(11), DataProvider: "import"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 5), Close: decf(12), DataProvider: "import"},
		{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 2, 1), Close: decf(20), DataProvider: "import"},
	}
	if err := p.UpsertPrices(ctx, prices); err != nil {
		t.Fatalf("upsert prices: %v", err)
	}

	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: d(2024, 1, 3), Before: d(2024, 1, 6)},
		{From: d(2024, 2, 1), Before: d(2024, 2, 2)},
	})
	assertCoverageContains(t, p)
}

// Two disjoint held periods stay disjoint: the hole between them is a fact
// about what was fetched, and merging would erase it.
func TestUpsertPricesForRange_DisjointPeriodsStayDisjoint(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	first := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2023, 1, 3), Close: decf(10)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", first, d(2023, 1, 1), d(2023, 7, 1), nil); err != nil {
		t.Fatalf("upsert first period: %v", err)
	}
	second := []db.EODPrice{{ListingID: pricedListing(t, p, instID), PriceDate: d(2024, 1, 3), Close: decf(20)}}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", second, d(2024, 1, 1), d(2024, 7, 1), nil); err != nil {
		t.Fatalf("upsert second period: %v", err)
	}

	assertRanges(t, readPriceCoverage(t, p, instID), []db.DateRange{
		{From: d(2023, 1, 1), Before: d(2023, 7, 1)},
		{From: d(2024, 1, 1), Before: d(2024, 7, 1)},
	})
	assertCoverageContains(t, p)
}

// Coverage is per plugin, so a range one plugin declined does not stop another
// being asked. Both spans coexist for the same instrument.
func TestUpsertPricesForRange_CoverageIsPerPlugin(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "massive", nil, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert massive: %v", err)
	}
	if err := p.UpsertPricesForRange(ctx, pricedListing(t, p, instID), "eodhd", nil, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert eodhd: %v", err)
	}

	var count int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(DISTINCT plugin_id) FROM price_coverage WHERE listing_id = $1::uuid
	`, pricedListing(t, p, instID)).Scan(&count); err != nil {
		t.Fatalf("count plugins: %v", err)
	}
	if count != 2 {
		t.Errorf("expected coverage for 2 plugins, got %d", count)
	}
}

// Coverage rides the security's cascade through its listings, so a merge cannot
// leave spans pointing at a line that no longer exists. The chain is two links
// now rather than one, which is what makes it worth pinning.
func TestPriceCoverage_CascadesOnInstrumentDelete(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")
	listingID := pricedListing(t, p, instID)

	if err := p.UpsertPricesForRange(ctx, listingID, "massive", nil, d(2024, 1, 1), d(2024, 1, 11), nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1::uuid`, instID); err != nil {
		t.Fatalf("delete instrument: %v", err)
	}

	if got := readListingCoverage(t, p, listingID); len(got) != 0 {
		t.Errorf("expected coverage to cascade away, got %+v", got)
	}
}
