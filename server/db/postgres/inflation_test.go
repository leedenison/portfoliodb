package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
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
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: 130.5, BaseYear: 2015, DataProvider: "ons"},
		{Currency: "GBP", Month: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), IndexValue: 131.0, BaseYear: 2015, DataProvider: "ons"},
	}

	if err := p.UpsertInflationIndices(ctx, indices); err != nil {
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
	indices[0].IndexValue = 130.8
	if err := p.UpsertInflationIndices(ctx, indices); err != nil {
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
	if rows[0].IndexValue != 131.0 {
		t.Errorf("expected 131.0, got %f", rows[0].IndexValue)
	}
	if rows[1].IndexValue != 130.8 {
		t.Errorf("expected 130.8 (updated), got %f", rows[1].IndexValue)
	}
}

func TestUpsertInflationIndices_Empty(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	if err := p.UpsertInflationIndices(ctx, nil); err != nil {
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
		{Currency: "GBP", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: 130.5, BaseYear: 2015, DataProvider: "ons"},
		{Currency: "GBP", Month: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), IndexValue: 132.0, BaseYear: 2015, DataProvider: "ons"},
		{Currency: "USD", Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: 310.0, BaseYear: 1982, DataProvider: "bls"},
	}
	if err := p.UpsertInflationIndices(ctx, indices); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Filter by currency.
	rows, _, total, err := p.ListInflationIndices(ctx, "GBP", nil, nil, 100, "")
	if err != nil {
		t.Fatalf("list GBP: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2, got %d", total)
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

	// No filter returns all.
	rows, _, total, err = p.ListInflationIndices(ctx, "", nil, nil, 100, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3, got %d", total)
	}
	_ = rows
}

func TestListInflationIndices_Pagination(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	var indices []db.InflationIndex
	for m := time.Month(1); m <= 5; m++ {
		indices = append(indices, db.InflationIndex{
			Currency: "GBP", Month: time.Date(2024, m, 1, 0, 0, 0, 0, time.UTC),
			IndexValue: 130.0 + float64(m), BaseYear: 2015, DataProvider: "ons",
		})
	}
	if err := p.UpsertInflationIndices(ctx, indices); err != nil {
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
