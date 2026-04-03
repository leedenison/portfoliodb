package main

import (
	"strings"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

func TestChunkRange(t *testing.T) {
	tests := []struct {
		name       string
		from, to   time.Time
		wantChunks int
	}{
		{
			name:       "short range (30 days)",
			from:       date(2024, 1, 1),
			to:         date(2024, 1, 31),
			wantChunks: 1,
		},
		{
			name:       "exactly 365 days (non-leap year)",
			from:       date(2023, 1, 1),
			to:         date(2024, 1, 1), // 365 days (2023 is not a leap year)
			wantChunks: 1,
		},
		{
			name:       "over 365 days splits into 2",
			from:       date(2023, 1, 1),
			to:         date(2024, 1, 2),
			wantChunks: 2,
		},
		{
			name:       "about 2 years",
			from:       date(2023, 1, 1),
			to:         date(2024, 12, 31),
			wantChunks: 2,
		},
		{
			name:       "empty range",
			from:       date(2024, 1, 1),
			to:         date(2024, 1, 1),
			wantChunks: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := chunkRange(tc.from, tc.to)
			if len(chunks) != tc.wantChunks {
				t.Fatalf("want %d chunks, got %d", tc.wantChunks, len(chunks))
			}
			// Verify chunks are contiguous and cover the full range.
			if len(chunks) > 0 {
				if !chunks[0].from.Equal(tc.from) {
					t.Fatalf("first chunk starts at %s, want %s", chunks[0].from, tc.from)
				}
				if !chunks[len(chunks)-1].to.Equal(tc.to) {
					t.Fatalf("last chunk ends at %s, want %s", chunks[len(chunks)-1].to, tc.to)
				}
				for i := 1; i < len(chunks); i++ {
					if !chunks[i].from.Equal(chunks[i-1].to) {
						t.Fatalf("gap between chunk %d end (%s) and chunk %d start (%s)", i-1, chunks[i-1].to, i, chunks[i].from)
					}
				}
			}
		})
	}
}

func TestGoogleFinanceFormula(t *testing.T) {
	tests := []struct {
		name         string
		ticker       string
		from, to     time.Time
		wantContains []string
	}{
		{
			name:   "stock",
			ticker: "NASDAQ:AAPL",
			from:   date(2024, 1, 1),
			to:     date(2024, 7, 1),
			wantContains: []string{
				`=GOOGLEFINANCE("NASDAQ:AAPL"`,
				`"close"`,
				`DATE(2024,1,1)`,
				`DATE(2024,6,30)`, // to is exclusive, so end date is June 30
				`"DAILY"`,
			},
		},
		{
			name:   "FX pair",
			ticker: "CURRENCY:GBPUSD",
			from:   date(2024, 3, 15),
			to:     date(2024, 4, 15),
			wantContains: []string{
				`"CURRENCY:GBPUSD"`,
				`DATE(2024,3,15)`,
				`DATE(2024,4,14)`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := googleFinanceFormula(tc.ticker, tc.from, tc.to)
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("formula %q missing %q", got, want)
				}
			}
		})
	}
}

func TestGenerateFormulas_StockAndFX(t *testing.T) {
	priceGaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-1",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Domain: "XNAS", Value: "AAPL"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_STOCK,
			Name:         "Apple Inc",
			Gaps: []*apiv1.DateRange{
				{From: "2024-01-01", To: "2024-07-01"},
			},
		},
	}
	fxGaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-fx",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_FX_PAIR, Value: "GBPUSD"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_FX,
			Name:         "GBPUSD",
			Gaps: []*apiv1.DateRange{
				{From: "2024-01-01", To: "2024-04-01"},
			},
		},
	}

	res := generateFormulas(priceGaps, fxGaps)
	if len(res.Skipped) != 0 {
		t.Fatalf("unexpected skips: %v", res.Skipped)
	}
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(res.Columns))
	}

	// Check stock column.
	stockCol := res.Columns[0]
	if !strings.Contains(stockCol.Header, "MIC_TICKER") || !strings.Contains(stockCol.Header, "AAPL") {
		t.Fatalf("unexpected stock header: %s", stockCol.Header)
	}
	if !strings.Contains(stockCol.Formula, "NASDAQ:AAPL") {
		t.Fatalf("formula missing ticker: %s", stockCol.Formula)
	}

	// Check FX column.
	fxCol := res.Columns[1]
	if !strings.Contains(fxCol.Header, "FX_PAIR") || !strings.Contains(fxCol.Header, "GBPUSD") {
		t.Fatalf("unexpected FX header: %s", fxCol.Header)
	}
	if !strings.Contains(fxCol.Formula, "CURRENCY:GBPUSD") {
		t.Fatalf("formula missing ticker: %s", fxCol.Formula)
	}
}

func TestGenerateFormulas_YearChunking(t *testing.T) {
	gaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-1",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Domain: "XNYS", Value: "IBM"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_STOCK,
			Name:         "IBM",
			Gaps: []*apiv1.DateRange{
				{From: "2023-01-01", To: "2024-12-31"}, // ~2 years, fits in 2 chunks
			},
		},
	}

	res := generateFormulas(gaps, nil)
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns (2 chunks), got %d", len(res.Columns))
	}
	// Both columns should have the same header (same instrument).
	if res.Columns[0].Header != res.Columns[1].Header {
		t.Fatalf("expected same header for both chunks")
	}
}

func TestGenerateFormulas_SkipsUnmappable(t *testing.T) {
	gaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-1",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_ISIN, Value: "US0378331005"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_STOCK,
			Name:         "Apple ISIN",
			Gaps: []*apiv1.DateRange{
				{From: "2024-01-01", To: "2024-07-01"},
			},
		},
	}

	res := generateFormulas(gaps, nil)
	if len(res.Columns) != 0 {
		t.Fatalf("expected 0 columns for unmappable, got %d", len(res.Columns))
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("expected 1 skip, got %d", len(res.Skipped))
	}
}

func TestGenerateFormulas_MultipleGapRanges(t *testing.T) {
	gaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-1",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Domain: "XNAS", Value: "TSLA"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_STOCK,
			Gaps: []*apiv1.DateRange{
				{From: "2024-01-01", To: "2024-03-01"},
				{From: "2024-06-01", To: "2024-09-01"},
			},
		},
	}

	res := generateFormulas(gaps, nil)
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns (2 gap ranges), got %d", len(res.Columns))
	}
	// Same instrument, same header.
	if res.Columns[0].Header != res.Columns[1].Header {
		t.Fatalf("expected same header for both ranges")
	}
}

func TestColumnsToGrid(t *testing.T) {
	cols := []sheetColumn{
		{Header: "H1", Formula: "=F1"},
		{Header: "H2", Formula: "=F2"},
	}

	grid := columnsToGrid(cols)
	if len(grid) != 2 { // header + formula
		t.Fatalf("expected 2 rows, got %d", len(grid))
	}
	if len(grid[0]) != 4 { // 2 columns * 2
		t.Fatalf("expected 4 grid columns, got %d", len(grid[0]))
	}
	if grid[0][0] != "H1" || grid[0][2] != "H2" {
		t.Fatalf("headers: got %q, %q", grid[0][0], grid[0][2])
	}
	if grid[1][0] != "=F1" || grid[1][2] != "=F2" {
		t.Fatalf("formulas: got %q, %q", grid[1][0], grid[1][2])
	}
}

func TestColumnsToGrid_Empty(t *testing.T) {
	grid := columnsToGrid(nil)
	if grid != nil {
		t.Fatalf("expected nil grid for no columns, got %v", grid)
	}
}

func TestGenerateFormulas_OpenfIGITickerWithExchange(t *testing.T) {
	gaps := []*apiv1.PriceGap{
		{
			InstrumentId: "inst-1",
			Identifier:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_OPENFIGI_TICKER, Domain: "US", Value: "MSFT"},
			AssetClass:   apiv1.AssetClass_ASSET_CLASS_STOCK,
			Exchange:     "XNAS",
			Name:         "Microsoft",
			Gaps:         []*apiv1.DateRange{{From: "2024-01-01", To: "2024-04-01"}},
		},
	}

	res := generateFormulas(gaps, nil)
	if len(res.Skipped) != 0 {
		t.Fatalf("unexpected skips: %v", res.Skipped)
	}
	if len(res.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(res.Columns))
	}
	if !strings.Contains(res.Columns[0].Formula, "NASDAQ:MSFT") {
		t.Fatalf("expected NASDAQ:MSFT in formula, got %s", res.Columns[0].Formula)
	}
}

func date(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}
