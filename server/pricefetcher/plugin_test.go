package pricefetcher

import (
	"testing"
	"time"
)

func TestRewriteFXPair(t *testing.T) {
	tests := []struct {
		input       string
		wantSource  string
		wantDivisor float64
	}{
		{"GBXUSD", "GBPUSD", 100},
		{"EURUSD", "EURUSD", 1},
		{"GBPUSD", "GBPUSD", 1},
		{"", "", 1},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			src, div := RewriteFXPair(tc.input)
			if src != tc.wantSource || div != tc.wantDivisor {
				t.Errorf("RewriteFXPair(%q) = (%q, %v), want (%q, %v)",
					tc.input, src, div, tc.wantSource, tc.wantDivisor)
			}
		})
	}
}

func TestScaleBars(t *testing.T) {
	o, h, l := 1.25, 1.27, 1.24
	v := int64(1000)
	bars := []DailyBar{
		{
			Date:   time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  1.26,
			Volume: &v,
		},
		{
			Date:  time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC),
			Close: 1.28,
			// Open, High, Low, Volume nil
		},
	}

	scaled := ScaleBars(bars, 100)

	if len(scaled) != 2 {
		t.Fatalf("len = %d, want 2", len(scaled))
	}

	// Bar 0: all fields present.
	b := scaled[0]
	if b.Close != 0.0126 {
		t.Errorf("bar[0].Close = %v, want 0.0126", b.Close)
	}
	if b.Open == nil || *b.Open != 0.0125 {
		t.Errorf("bar[0].Open = %v, want 0.0125", b.Open)
	}
	if b.High == nil || *b.High != 0.0127 {
		t.Errorf("bar[0].High = %v, want 0.0127", b.High)
	}
	if b.Low == nil || *b.Low != 0.0124 {
		t.Errorf("bar[0].Low = %v, want 0.0124", b.Low)
	}
	if b.Volume == nil || *b.Volume != 1000 {
		t.Errorf("bar[0].Volume = %v, want 1000", b.Volume)
	}

	// Bar 1: nil optional fields stay nil.
	b = scaled[1]
	if b.Close != 0.0128 {
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

	// Original bars unchanged.
	if bars[0].Close != 1.26 {
		t.Error("original bars mutated")
	}
}
