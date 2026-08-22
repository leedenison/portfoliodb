package pricefetcher

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestRewriteFXPair(t *testing.T) {
	tests := []struct {
		input      string
		wantSource string
		wantExp    int32
	}{
		{"GBXUSD", "GBPUSD", -2},
		{"EURUSD", "EURUSD", 0},
		{"GBPUSD", "GBPUSD", 0},
		{"", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			src, exp := RewriteFXPair(tc.input)
			if src != tc.wantSource || exp != tc.wantExp {
				t.Errorf("RewriteFXPair(%q) = (%q, %v), want (%q, %v)",
					tc.input, src, exp, tc.wantSource, tc.wantExp)
			}
		})
	}
}

// DerivedFXPairs is built from currency.MinorUnits, and every entry is a pair a
// price plugin has to be able to synthesise from its source. A currency added to
// that table silently adds one here, so pin what it holds.
func TestDerivedFXPairs_holdsOnlyGBXUSD(t *testing.T) {
	want := map[string]DerivedFXPair{
		"GBXUSD": {SourcePair: "GBPUSD", Exponent: -2},
	}
	if len(DerivedFXPairs) != len(want) {
		t.Fatalf("DerivedFXPairs = %+v, want %+v", DerivedFXPairs, want)
	}
	for pair, d := range want {
		if DerivedFXPairs[pair] != d {
			t.Errorf("DerivedFXPairs[%q] = %+v, want %+v", pair, DerivedFXPairs[pair], d)
		}
	}
}

func TestScaleBars(t *testing.T) {
	o := decimal.RequireFromString("1.25")
	h := decimal.RequireFromString("1.27")
	l := decimal.RequireFromString("1.24")
	v := int64(1000)
	ac := decimal.RequireFromString("1.26")
	bars := []DailyBar{
		{
			Date:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  decimal.RequireFromString("1.26"),
			Volume: &v,
			// A price in the same currency as the rest, so it shifts with them.
			// EODHD supplies one on every bar, including the FX series a derived
			// pair is scaled from.
			AdjustedClose: &ac,
		},
		{
			Date:  time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
			Close: decimal.RequireFromString("1.28"),
			// Open, High, Low, Volume, AdjustedClose nil
		},
	}

	scaled := ScaleBars(bars, -2)

	if len(scaled) != 2 {
		t.Fatalf("len = %d, want 2", len(scaled))
	}

	// Bar 0: all fields present.
	b := scaled[0]
	if b.Close.String() != "0.0126" {
		t.Errorf("bar[0].Close = %v, want 0.0126", b.Close)
	}
	if b.Open == nil || b.Open.String() != "0.0125" {
		t.Errorf("bar[0].Open = %v, want 0.0125", b.Open)
	}
	if b.High == nil || b.High.String() != "0.0127" {
		t.Errorf("bar[0].High = %v, want 0.0127", b.High)
	}
	if b.Low == nil || b.Low.String() != "0.0124" {
		t.Errorf("bar[0].Low = %v, want 0.0124", b.Low)
	}
	if b.Volume == nil || *b.Volume != 1000 {
		t.Errorf("bar[0].Volume = %v, want 1000", b.Volume)
	}
	if b.AdjustedClose == nil || b.AdjustedClose.String() != "0.0126" {
		t.Errorf("bar[0].AdjustedClose = %v, want 0.0126", b.AdjustedClose)
	}

	// Bar 1: nil optional fields stay nil.
	b = scaled[1]
	if b.Close.String() != "0.0128" {
		t.Errorf("bar[1].Close = %v, want 0.0128", b.Close)
	}
	if b.Open != nil {
		t.Errorf("bar[1].Open should be nil")
	}
	if b.High != nil {
		t.Errorf("bar[1].High should be nil")
	}
	if b.Low != nil {
		t.Errorf("bar[1].Low should be nil")
	}
	if b.Volume != nil {
		t.Errorf("bar[1].Volume should be nil")
	}
	if b.AdjustedClose != nil {
		t.Errorf("bar[1].AdjustedClose should be nil")
	}

	// Original bars unchanged.
	if bars[0].Close.String() != "1.26" {
		t.Error("original bars mutated")
	}
}
