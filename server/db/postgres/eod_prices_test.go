package postgres

import (
	"context"
	"testing"
	"time"
)

// insertPriceWithProvider inserts a single eod_prices row with a specific provider.
func insertPriceWithProvider(t *testing.T, p *Postgres, instID string, priceDate time.Time, close float64, provider string) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, close, data_provider)
		VALUES ($1::uuid, $2, $3, $4)
	`, instID, priceDate, close, provider)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
}

// insertPriceFull inserts a price row with all OHLCV fields.
func insertPriceFull(t *testing.T, p *Postgres, instID string, priceDate time.Time, open, high, low, close float64, volume int64, provider string) {
	t.Helper()
	ctx := context.Background()
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)
	`, instID, priceDate, open, high, low, close, volume, provider)
	if err != nil {
		t.Fatalf("insert price: %v", err)
	}
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
	if fullRow.Open == nil || *fullRow.Open != 100 {
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
