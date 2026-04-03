package main

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

func TestParseExchangeCSV(t *testing.T) {
	m := parseExchangeCSV(exchangeCSV)
	if len(m) == 0 {
		t.Fatal("parsed zero exchange mappings")
	}
	// Spot-check a few well-known mappings.
	tests := []struct {
		mic  string
		want string
	}{
		{"XNAS", "NASDAQ"},
		{"XNYS", "NYSE"},
		{"XLON", "LON"},
		{"XPAR", "EPA"},
		{"XETR", "ETR"},
		{"XHKG", "HKG"},
		{"XTKS", "TYO"},
		{"XASX", "ASX"},
		{"XTSE", "TSE"},
		{"XJSE", "JSE"},
		{"XTAE", "TLV"},
	}
	for _, tc := range tests {
		t.Run(tc.mic, func(t *testing.T) {
			got, ok := m[tc.mic]
			if !ok {
				t.Fatalf("MIC %s not found in map", tc.mic)
			}
			if got != tc.want {
				t.Fatalf("MIC %s: want %s, got %s", tc.mic, tc.want, got)
			}
		})
	}
}

func TestGfTicker(t *testing.T) {
	tests := []struct {
		name        string
		ident       *apiv1.InstrumentIdentifier
		exchangeMIC string
		want        string
		wantErr     bool
	}{
		{
			name:  "MIC_TICKER with domain",
			ident: &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Domain: "XNAS", Value: "AAPL"},
			want:  "NASDAQ:AAPL",
		},
		{
			name:        "MIC_TICKER without domain falls back to exchange",
			ident:       &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Value: "VOD"},
			exchangeMIC: "XLON",
			want:        "LON:VOD",
		},
		{
			name:    "MIC_TICKER unknown MIC",
			ident:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_MIC_TICKER, Domain: "XXXX", Value: "FOO"},
			wantErr: true,
		},
		{
			name:  "FX_PAIR",
			ident: &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_FX_PAIR, Value: "GBPUSD"},
			want:  "CURRENCY:GBPUSD",
		},
		{
			name:        "OPENFIGI_TICKER with exchange MIC",
			ident:       &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_OPENFIGI_TICKER, Domain: "US", Value: "MSFT"},
			exchangeMIC: "XNAS",
			want:        "NASDAQ:MSFT",
		},
		{
			name:    "OPENFIGI_TICKER without exchange MIC",
			ident:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_OPENFIGI_TICKER, Domain: "US", Value: "MSFT"},
			wantErr: true,
		},
		{
			name:    "unsupported identifier type",
			ident:   &apiv1.InstrumentIdentifier{Type: apiv1.IdentifierType_ISIN, Value: "US0378331005"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gfTicker(tc.ident, tc.exchangeMIC)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestMICToGF(t *testing.T) {
	gf, ok := MICToGF("XNYS")
	if !ok || gf != "NYSE" {
		t.Fatalf("XNYS: want NYSE, got %s (ok=%v)", gf, ok)
	}
	_, ok = MICToGF("ZZZZ")
	if ok {
		t.Fatal("expected unknown MIC to return false")
	}
}
