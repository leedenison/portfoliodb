package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

func setupUserWithCurrency(t *testing.T, p *Postgres, authSub, name, email, displayCurrency string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	err := p.q.QueryRowContext(ctx, `
		INSERT INTO users (id, auth_sub, name, email, display_currency)
		VALUES (gen_random_uuid(), $1, $2, $3, $4)
		RETURNING id
	`, authSub, name, email, displayCurrency).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return userID
}

func TestDistinctDisplayCurrencies_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	currencies, err := p.DistinctDisplayCurrencies(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(currencies) != 0 {
		t.Fatalf("expected empty, got %v", currencies)
	}
}

func TestDistinctDisplayCurrencies(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	setupUserWithCurrency(t, p, "sub1", "Alice", "alice@example.com", "GBP")
	setupUserWithCurrency(t, p, "sub2", "Bob", "bob@example.com", "USD")
	setupUserWithCurrency(t, p, "sub3", "Carol", "carol@example.com", "GBP") // duplicate

	currencies, err := p.DistinctDisplayCurrencies(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(currencies) != 2 {
		t.Fatalf("expected 2 currencies, got %v", currencies)
	}
	if currencies[0] != "GBP" || currencies[1] != "USD" {
		t.Fatalf("expected [GBP USD], got %v", currencies)
	}
}

func TestUpsertInflationIndices(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	indices := []db.InflationIndex{
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decf(130.5), BaseYear: 2015, DataProvider: "ons"},
		{Currency: "GBP", Month: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), IndexValue: decf(131.0), BaseYear: 2015, DataProvider: "ons"},
	}

	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify coverage.
	months, err := p.InflationCoverage(ctx, "GBP")
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if len(months) != 2 {
		t.Fatalf("expected 2 months, got %d", len(months))
	}

	// Upsert with updated value should overwrite.
	indices[0].IndexValue = decf(130.8)
	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	rows, _, total, err := p.ListInflationIndices(ctx, "GBP", nil, nil, 100, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
	// Ordered by month DESC, so Feb first.
	if rows[0].IndexValue.String() != "131" {
		t.Errorf("expected 131, got %v", rows[0].IndexValue)
	}
	if rows[1].IndexValue.String() != "130.8" {
		t.Errorf("expected 130.8 (updated), got %v", rows[1].IndexValue)
	}
}

func TestUpsertInflationIndices_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	if err := p.UpsertInflationIndices(ctx, nil, nil); err != nil {
		t.Fatalf("upsert empty: %v", err)
	}
}

func TestInflationCoverage_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	months, err := p.InflationCoverage(ctx, "GBP")
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if len(months) != 0 {
		t.Fatalf("expected empty, got %v", months)
	}
}

func TestListInflationIndices_Filters(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	indices := []db.InflationIndex{
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decf(130.5), BaseYear: 2015, DataProvider: "ons"},
		{Currency: "GBP", Month: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), IndexValue: decf(132.0), BaseYear: 2015, DataProvider: "ons"},
		{Currency: "USD", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decf(310.0), BaseYear: 1982, DataProvider: "bls"},
	}
	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Filter by currency.
	rows, _, total, err := p.ListInflationIndices(ctx, "GBP", nil, nil, 100, "")
	if err != nil {
		t.Fatalf("list GBP: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("expected 2, got total=%d rows=%d", total, len(rows))
	}

	// Filter by date range.
	from := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	rows, _, total, err = p.ListInflationIndices(ctx, "", &from, nil, 100, "")
	if err != nil {
		t.Fatalf("list from March: %v", err)
	}
	if total != 1 || rows[0].Currency != "GBP" {
		t.Fatalf("expected 1 GBP row from March+, got total=%d", total)
	}

	// The upper bound is exclusive: a month on it is out.
	before := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	rows, _, total, err = p.ListInflationIndices(ctx, "GBP", nil, &before, 100, "")
	if err != nil {
		t.Fatalf("list before June: %v", err)
	}
	if total != 1 || !rows[0].Month.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected only the January row, got total=%d rows=%v", total, rows)
	}

	// No filter returns all.
	rows, _, total, err = p.ListInflationIndices(ctx, "", nil, nil, 100, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("expected 3, got total=%d rows=%d", total, len(rows))
	}
}

func TestListInflationIndices_Pagination(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	var indices []db.InflationIndex
	for m := time.Month(1); m <= 5; m++ {
		indices = append(indices, db.InflationIndex{
			Currency: "GBP", Month: time.Date(2024, m, 1, 0, 0, 0, 0, time.UTC),
			IndexValue: decf(130).Add(decimal.NewFromInt(int64(m))), BaseYear: 2015, DataProvider: "ons",
		})
	}
	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Page 1.
	rows, nextToken, total, err := p.ListInflationIndices(ctx, "", nil, nil, 2, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 5 || len(rows) != 2 || nextToken == "" {
		t.Fatalf("page 1: total=%d rows=%d token=%q", total, len(rows), nextToken)
	}

	// Page 2.
	rows, nextToken, _, err = p.ListInflationIndices(ctx, "", nil, nil, 2, nextToken)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(rows) != 2 || nextToken == "" {
		t.Fatalf("page 2: rows=%d token=%q", len(rows), nextToken)
	}

	// Page 3 (last).
	rows, nextToken, _, err = p.ListInflationIndices(ctx, "", nil, nil, 2, nextToken)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(rows) != 1 || nextToken != "" {
		t.Fatalf("page 3: rows=%d token=%q", len(rows), nextToken)
	}
}

// The export lists every row across every currency, ordered so that the API
// layer can group by a scan.
func TestListInflationIndicesForExport(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	indices := []db.InflationIndex{
		{Currency: "USD", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decimal.RequireFromString("308.4"), BaseYear: 1984, DataProvider: "bls"},
		{Currency: "GBP", Month: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), IndexValue: decimal.RequireFromString("132.1"), BaseYear: 2015, DataProvider: "ons"},
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decimal.RequireFromString("131.5"), BaseYear: 2015, DataProvider: "ons"},
	}
	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rows, err := p.ListInflationIndicesForExport(ctx)
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	want := []struct {
		currency string
		month    time.Time
	}{
		{"GBP", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"GBP", time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"USD", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for i, w := range want {
		if rows[i].Currency != w.currency || !rows[i].Month.UTC().Equal(w.month) {
			t.Fatalf("row %d = %s %v, want %s %v", i, rows[i].Currency, rows[i].Month, w.currency, w.month)
		}
	}
	if !rows[0].IndexValue.Equal(decimal.RequireFromString("131.5")) {
		t.Fatalf("index value = %s", rows[0].IndexValue)
	}
}

// An import stamps the envelope's knowledge time rather than now, so a restored
// series is as stale as the file it came from and the fetcher still refreshes
// it.
func TestUpsertInflationIndices_StampsSuppliedFetchedAt(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	asOf := time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)
	indices := []db.InflationIndex{
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: decimal.RequireFromString("131.5"), BaseYear: 2015, DataProvider: "import"},
	}
	if err := p.UpsertInflationIndices(ctx, indices, &asOf); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var stored time.Time
	if err := p.q.QueryRowContext(ctx,
		`SELECT last_fetched_at FROM inflation_indices WHERE currency = 'GBP'`).Scan(&stored); err != nil {
		t.Fatalf("read last_fetched_at: %v", err)
	}
	if !stored.UTC().Equal(asOf) {
		t.Fatalf("last_fetched_at = %v, want %v", stored.UTC(), asOf)
	}

	// A refetch with no stamp moves it forward again.
	if err := p.UpsertInflationIndices(ctx, indices, nil); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if err := p.q.QueryRowContext(ctx,
		`SELECT last_fetched_at FROM inflation_indices WHERE currency = 'GBP'`).Scan(&stored); err != nil {
		t.Fatalf("read last_fetched_at: %v", err)
	}
	if !stored.After(asOf) {
		t.Fatalf("last_fetched_at = %v, expected it to move forward", stored)
	}
}
