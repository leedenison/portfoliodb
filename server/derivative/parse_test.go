package derivative

import (
	"testing"
	"time"
)

func TestParseOptionTicker(t *testing.T) {
	tests := []struct {
		name      string
		ticker    string
		wantFormat string
		wantSymbol string
		wantExpiry time.Time
		wantPutCall string
		wantStrike  float64
		ok        bool
	}{
		{
			name:       "OCC 21-char",
			ticker:     "AAPL  250117C00150000",
			wantFormat: FormatOCC,
			wantSymbol: "AAPL",
			wantExpiry: time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC),
			wantPutCall: "C",
			wantStrike:  150,
			ok:         true,
		},
		{
			name:       "OCC put",
			ticker:     "SPY   250117P00600000",
			wantFormat: FormatOCC,
			wantSymbol: "SPY",
			wantExpiry: time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC),
			wantPutCall: "P",
			wantStrike:  600,
			ok:         true,
		},
		{
			name:       "Classic",
			ticker:     "IBM 03/20/10 C105",
			wantFormat: FormatClassic,
			wantSymbol: "IBM",
			wantExpiry: time.Date(2010, 3, 20, 0, 0, 0, 0, time.UTC),
			wantPutCall: "C",
			wantStrike:  105,
			ok:         true,
		},
		{
			name:       "Classic put",
			ticker:     "IBM 03/20/10 P 105",
			wantFormat: FormatClassic,
			wantSymbol: "IBM",
			wantExpiry: time.Date(2010, 3, 20, 0, 0, 0, 0, time.UTC),
			wantPutCall: "P",
			wantStrike:  105,
			ok:         true,
		},
		{
			name:       "Compact",
			ticker:     "AAPL20250117C200",
			wantFormat: FormatCompact,
			wantSymbol: "AAPL",
			wantExpiry: time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC),
			wantPutCall: "C",
			wantStrike:  200,
			ok:         true,
		},
		{
			name:       "OCC compact (no spaces)",
			ticker:     "AAPL250117C00150000",
			wantFormat: FormatOCC,
			wantSymbol: "AAPL",
			wantExpiry: time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC),
			wantPutCall: "C",
			wantStrike:  150,
			ok:         true,
		},
		{
			name:       "OCC compact 1-char root",
			ticker:     "A250117C00150000",
			wantFormat: FormatOCC,
			wantSymbol: "A",
			wantExpiry: time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC),
			wantPutCall: "C",
			wantStrike:  150,
			ok:         true,
		},
		{
			name:       "lowercase OCC",
			ticker:     "aapl  250117c00150000",
			wantFormat: FormatOCC,
			wantSymbol: "AAPL",
			wantPutCall: "C",
			wantStrike:  150,
			ok:         true,
		},
		{"empty", "", "", "", time.Time{}, "", 0, false},
		{"whitespace only", "  \t  ", "", "", time.Time{}, "", 0, false},
		{"garbage", "XYZ???", "", "", time.Time{}, "", 0, false},
		{"too short for OCC", "AAPL 250117C0015000", "", "", time.Time{}, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseOptionTicker(tt.ticker)
			if ok != tt.ok {
				t.Errorf("ParseOptionTicker() ok = %v, want %v", ok, tt.ok)
				return
			}
			if !tt.ok {
				return
			}
			if got.Format != tt.wantFormat {
				t.Errorf("Format = %q, want %q", got.Format, tt.wantFormat)
			}
			if got.Symbol != tt.wantSymbol {
				t.Errorf("Symbol = %q, want %q", got.Symbol, tt.wantSymbol)
			}
			if !tt.wantExpiry.IsZero() && !got.Expiry.Equal(tt.wantExpiry) {
				t.Errorf("Expiry = %v, want %v", got.Expiry, tt.wantExpiry)
			}
			if got.PutCall != tt.wantPutCall {
				t.Errorf("PutCall = %q, want %q", got.PutCall, tt.wantPutCall)
			}
			if got.Strike != tt.wantStrike {
				t.Errorf("Strike = %v, want %v", got.Strike, tt.wantStrike)
			}
		})
	}
}

func TestBuildOCCCompact(t *testing.T) {
	tests := []struct {
		name    string
		symbol  string
		expiry  time.Time
		putCall string
		strike  float64
		want    string
		ok      bool
	}{
		{"call", "AAPL", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "C", 200, "AAPL251219C00200000", true},
		{"put", "SPY", time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC), "P", 600, "SPY250117P00600000", true},
		{"fractional strike", "IBM", time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC), "C", 105.5, "IBM250320C00105500", true},
		{"1-char symbol", "A", time.Date(2025, 6, 20, 0, 0, 0, 0, time.UTC), "C", 50, "A250620C00050000", true},
		{"6-char symbol", "BRKB12", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "P", 230, "BRKB12251219P00230000", true},
		{"lowercase input", "aapl", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "c", 200, "AAPL251219C00200000", true},
		{"empty symbol", "", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "C", 200, "", false},
		{"symbol too long", "TOOLONG", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "C", 200, "", false},
		{"bad putCall", "AAPL", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "X", 200, "", false},
		{"zero expiry", "AAPL", time.Time{}, "C", 200, "", false},
		{"negative strike", "AAPL", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "C", -1, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := BuildOCCCompact(tt.symbol, tt.expiry, tt.putCall, tt.strike)
			if ok != tt.ok {
				t.Errorf("BuildOCCCompact() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("BuildOCCCompact() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildOCCCompact_RoundTrip(t *testing.T) {
	// Build a compact OCC, then parse it back directly — fields should match.
	sym, expiry, pc, strike := "AAPL", time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC), "C", 200.0
	occ, ok := BuildOCCCompact(sym, expiry, pc, strike)
	if !ok {
		t.Fatal("BuildOCCCompact failed")
	}
	// Parse compact form directly (no padding needed).
	parsed, ok := ParseOptionTicker(occ)
	if !ok {
		t.Fatalf("ParseOptionTicker failed on compact OCC %q", occ)
	}
	if parsed.Symbol != sym {
		t.Errorf("Symbol = %q, want %q", parsed.Symbol, sym)
	}
	if !parsed.Expiry.Equal(expiry) {
		t.Errorf("Expiry = %v, want %v", parsed.Expiry, expiry)
	}
	if parsed.PutCall != pc {
		t.Errorf("PutCall = %q, want %q", parsed.PutCall, pc)
	}
	if parsed.Strike != strike {
		t.Errorf("Strike = %v, want %v", parsed.Strike, strike)
	}
}

func TestOCCCompact(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"padded 21-char", "AAPL  251219C00230000", "AAPL251219C00230000", true},
		{"already compact", "AAPL251219C00230000", "AAPL251219C00230000", true},
		{"1-char root padded", "A     251219C00230000", "A251219C00230000", true},
		{"6-char root", "BRKB12251219C00230000", "BRKB12251219C00230000", true},
		{"put option", "SPY   251219P00600000", "SPY251219P00600000", true},
		{"lowercase", "aapl  251219c00230000", "AAPL251219C00230000", true},
		{"empty", "", "", false},
		{"too short", "A25121", "", false},
		{"bad suffix", "AAPL251219X00230000", "", false},
		{"root too long", "ABCDEFG251219C00230000", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := OCCCompact(tt.in)
			if ok != tt.ok {
				t.Errorf("OCCCompact(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("OCCCompact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOCCPadded(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"compact to padded", "AAPL251219C00230000", "AAPL  251219C00230000", true},
		{"already padded", "AAPL  251219C00230000", "AAPL  251219C00230000", true},
		{"1-char root", "A251219C00230000", "A     251219C00230000", true},
		{"6-char root no pad needed", "BRKB12251219C00230000", "BRKB12251219C00230000", true},
		{"put option", "SPY251219P00600000", "SPY   251219P00600000", true},
		{"lowercase", "aapl251219c00230000", "AAPL  251219C00230000", true},
		{"empty", "", "", false},
		{"invalid", "garbage", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := OCCPadded(tt.in)
			if ok != tt.ok {
				t.Errorf("OCCPadded(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("OCCPadded(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
