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
