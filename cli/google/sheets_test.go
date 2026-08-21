package main

import (
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"os"
	"path/filepath"
	"testing"
)

func TestParseHeader(t *testing.T) {
	tests := []struct {
		name       string
		hdr        string
		wantType   string
		wantDomain string
		wantValue  string
		wantAC     string
		wantErr    bool
	}{
		{
			name:       "MIC_TICKER",
			hdr:        "MIC_TICKER|XNAS|AAPL|STOCK",
			wantType:   "MIC_TICKER",
			wantDomain: "XNAS",
			wantValue:  "AAPL",
			wantAC:     "STOCK",
		},
		{
			name:       "FX_PAIR with empty domain",
			hdr:        "FX_PAIR||GBPUSD|FX",
			wantType:   "FX_PAIR",
			wantDomain: "",
			wantValue:  "GBPUSD",
			wantAC:     "FX",
		},
		{
			name:    "too few fields",
			hdr:     "MIC_TICKER|XNAS|AAPL",
			wantErr: true,
		},
		{
			name:    "too many fields",
			hdr:     "MIC_TICKER|XNAS|AAPL|STOCK|extra",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idType, domain, value, ac, err := parseHeader(tc.hdr)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if idType != tc.wantType || domain != tc.wantDomain || value != tc.wantValue || ac != tc.wantAC {
				t.Fatalf("got (%s, %s, %s, %s), want (%s, %s, %s, %s)",
					idType, domain, value, ac, tc.wantType, tc.wantDomain, tc.wantValue, tc.wantAC)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "ISO format", input: "2024-06-15", want: "2024-06-15"},
		{name: "US format", input: "6/15/2024", want: "2024-06-15"},
		{name: "US padded", input: "06/15/2024", want: "2024-06-15"},
		{name: "serial number", input: "45458", want: "2024-06-15"},
		{name: "serial number float", input: "45458.0", want: "2024-06-15"},
		{name: "US with time", input: "08/08/2023 23:58:00", want: "2023-08-08"},
		{name: "US padded with time", input: "01/15/2024 23:58:00", want: "2024-01-15"},
		{name: "DD/MM/YYYY with time", input: "13/08/2023 23:58:00", want: "2023-08-13"},
		{name: "DD/MM/YYYY padded with time", input: "25/12/2024 23:58:00", want: "2024-12-25"},
		{name: "DD/MM/YYYY no time", input: "31/01/2024", want: "2024-01-31"},
		{name: "ISO with time", input: "2024-06-15 14:30:00", want: "2024-06-15"},
		{name: "with whitespace", input: "  2024-06-15  ", want: "2024-06-15"},
		{name: "invalid", input: "not-a-date", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDate(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %s, got %s", tc.want, got)
			}
		})
	}
}

func TestCellString(t *testing.T) {
	row := []any{"hello", 42.0, 3.14, nil}

	tests := []struct {
		col    int
		want   string
		wantOK bool
	}{
		{0, "hello", true},
		{1, "42", true},   // integer-valued float
		{2, "3.14", true}, // fractional float
		{3, "", false},    // nil cell
		{4, "", false},    // out of bounds
	}
	for _, tc := range tests {
		got, ok := cellString(row, tc.col)
		if ok != tc.wantOK {
			t.Fatalf("col %d: ok=%v, want %v", tc.col, ok, tc.wantOK)
		}
		if ok && got != tc.want {
			t.Fatalf("col %d: got %q, want %q", tc.col, got, tc.want)
		}
	}
}

func TestParseOutputData(t *testing.T) {
	// Simulate evaluated GOOGLEFINANCE output pasted as values.
	// Three column pairs: stock, FX, and an all-#N/A range.
	data := [][]any{
		// Row 0: identifier headers
		{"MIC_TICKER|XNAS|AAPL|STOCK", "", "FX_PAIR||GBPUSD|FX", "", "MIC_TICKER|XLON|XXX|STOCK", ""},
		// Row 1: GOOGLEFINANCE header row ("Date", "Close") — should be skipped
		{"Date", "Close", "Date", "Close", "#N/A", ""},
		// Row 2: valid data
		{"2024-01-02", 185.5, "2024-01-02", 1.27, "", ""},
		// Row 3: normal data
		{"2024-01-03", 186.0, "2024-01-03", 1.28, "", ""},
		// Row 4: missing FX data
		{"2024-01-04", 187.0, "", "", "", ""},
	}

	groups, _ := parseOutputData(data)

	// One group per column pair, and the all-#N/A column produces none.
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	stock, fx := groups[0], groups[1]
	if stock.GetInstrument().GetType() != typev1.IdentifierType_MIC_TICKER {
		t.Fatalf("expected MIC_TICKER, got %s", stock.GetInstrument().GetType())
	}
	if fx.GetInstrument().GetType() != typev1.IdentifierType_FX_PAIR {
		t.Fatalf("expected FX_PAIR, got %s", fx.GetInstrument().GetType())
	}
	if len(stock.GetRows()) != 3 { // rows 2,3,4
		t.Fatalf("expected 3 stock prices, got %d", len(stock.GetRows()))
	}
	if len(fx.GetRows()) != 2 { // rows 2,3
		t.Fatalf("expected 2 FX prices, got %d", len(fx.GetRows()))
	}

	// Verify first stock price (row 2, after skipping Date/Close header).
	row := stock.GetRows()[0]
	if row.GetPriceDate() != "2024-01-02" {
		t.Fatalf("expected date 2024-01-02, got %s", row.GetPriceDate())
	}
	if row.GetClose() != "185.5" {
		t.Fatalf("expected close 185.5, got %v", row.GetClose())
	}
	if stock.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Fatalf("expected STOCK, got %s", stock.GetAssetClass())
	}
	// GOOGLEFINANCE back-adjusts, so every bar says which share count it is
	// denominated in. Silence would read as as-traded and be adjusted twice.
	if row.ShareCountBasis == nil {
		t.Fatal("expected a share_count_basis on every bar")
	}
}

func TestParseOutputData_AllNA(t *testing.T) {
	// All formulas returned #N/A (non-trading days only).
	data := [][]any{
		{"MIC_TICKER|XNAS|AAPL|STOCK", ""},
		{"#N/A", ""},
	}
	groups, _ := parseOutputData(data)
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups for all-#N/A range, got %d", len(groups))
	}
}

func TestParseOutputValues_EmptyData(t *testing.T) {
	data := [][]any{{"MIC_TICKER|XNAS|AAPL|STOCK", ""}}
	_, _, err := parseOutputValues(data)
	if err == nil {
		t.Fatal("expected error for single-row data")
	}
}

func TestStateCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &stateCache{SpreadsheetID: "abc123"}
	if err := saveState(dir, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.SpreadsheetID != "abc123" {
		t.Fatalf("want abc123, got %s", loaded.SpreadsheetID)
	}

	// Verify permissions.
	info, _ := os.Stat(filepath.Join(dir, "state.json"))
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected 0600, got %o", perm)
	}
}

func TestStateCacheMissing(t *testing.T) {
	_, err := loadState(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing state")
	}
}

// Coverage arrives keyed by instrument and has to land on the matching group:
// an archive states a span inside the group it applies to, and a span that
// misses its group would leave the non-trading days inside it unpriced.
func TestAttachCoverage(t *testing.T) {
	groups := []*archivev1.PriceGroup{
		{Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"}},
		{Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_FX_PAIR, Value: "GBPUSD"}},
	}
	attachCoverage(groups, map[db.InstrumentRef][]*archivev1.DateInterval{
		{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"}: {{From: "2024-01-01", Before: "2024-02-01"}},
	})

	if len(groups[0].GetCoverage()) != 1 {
		t.Fatalf("expected the AAPL group to carry 1 span, got %d", len(groups[0].GetCoverage()))
	}
	if groups[0].GetCoverage()[0].GetBefore() != "2024-02-01" {
		t.Fatalf("got span before %q", groups[0].GetCoverage()[0].GetBefore())
	}
	if len(groups[1].GetCoverage()) != 0 {
		t.Fatalf("expected the unmatched group to carry no spans, got %d", len(groups[1].GetCoverage()))
	}
}
