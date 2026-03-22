package db

import (
	"testing"
	"time"
)

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func TestMergeRanges(t *testing.T) {
	tests := []struct {
		name   string
		input  []DateRange
		expect []DateRange
	}{
		{
			name:   "empty",
			input:  nil,
			expect: nil,
		},
		{
			name:   "single",
			input:  []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
		},
		{
			name: "no overlap",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 10), d(2024, 1, 15)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 10), d(2024, 1, 15)},
			},
		},
		{
			name: "overlapping",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 10)},
				{d(2024, 1, 5), d(2024, 1, 15)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 15)},
			},
		},
		{
			name: "adjacent",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 5), d(2024, 1, 10)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 10)},
			},
		},
		{
			name: "unsorted input",
			input: []DateRange{
				{d(2024, 1, 10), d(2024, 1, 15)},
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 4), d(2024, 1, 11)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 15)},
			},
		},
		{
			name: "contained",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 20)},
				{d(2024, 1, 5), d(2024, 1, 10)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 20)},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeRanges(tc.input)
			assertRangesEqual(t, tc.expect, got)
		})
	}
}

func TestBridgeRanges(t *testing.T) {
	tests := []struct {
		name   string
		input  []DateRange
		maxGap int
		expect []DateRange
	}{
		{
			name:   "empty",
			input:  nil,
			maxGap: 3,
			expect: nil,
		},
		{
			name:   "single",
			input:  []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
			maxGap: 3,
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
		},
		{
			name: "weekend gap bridged",
			input: []DateRange{
				{d(2024, 1, 5), d(2024, 1, 6)}, // Fri
				{d(2024, 1, 8), d(2024, 1, 9)}, // Mon
			},
			maxGap: 3,
			expect: []DateRange{{d(2024, 1, 5), d(2024, 1, 9)}},
		},
		{
			name: "3-day gap bridged",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 8), d(2024, 1, 12)},
			},
			maxGap: 3,
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 12)}},
		},
		{
			name: "4-day gap not bridged",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 9), d(2024, 1, 12)},
			},
			maxGap: 3,
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 9), d(2024, 1, 12)},
			},
		},
		{
			name: "multiple bridges",
			input: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 6)},  // Mon-Sat
				{d(2024, 1, 8), d(2024, 1, 13)},  // Mon-Sat
				{d(2024, 1, 15), d(2024, 1, 20)}, // Mon-Sat
			},
			maxGap: 3,
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 20)}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BridgeRanges(tc.input, tc.maxGap)
			assertRangesEqual(t, tc.expect, got)
		})
	}
}

func TestSubtractRanges(t *testing.T) {
	tests := []struct {
		name   string
		needed []DateRange
		cached []DateRange
		expect []DateRange
	}{
		{
			name:   "no cached",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			cached: nil,
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
		},
		{
			name:   "fully cached",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			cached: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			expect: nil,
		},
		{
			name:   "cached superset",
			needed: []DateRange{{d(2024, 1, 3), d(2024, 1, 7)}},
			cached: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			expect: nil,
		},
		{
			name:   "gap at start",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			cached: []DateRange{{d(2024, 1, 5), d(2024, 1, 10)}},
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
		},
		{
			name:   "gap at end",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			cached: []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
			expect: []DateRange{{d(2024, 1, 5), d(2024, 1, 10)}},
		},
		{
			name:   "gap in middle",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 20)}},
			cached: []DateRange{{d(2024, 1, 5), d(2024, 1, 15)}},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 15), d(2024, 1, 20)},
			},
		},
		{
			name:   "multiple cached ranges",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 30)}},
			cached: []DateRange{
				{d(2024, 1, 5), d(2024, 1, 10)},
				{d(2024, 1, 15), d(2024, 1, 20)},
			},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 10), d(2024, 1, 15)},
				{d(2024, 1, 20), d(2024, 1, 30)},
			},
		},
		{
			name: "multiple needed ranges",
			needed: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 10)},
				{d(2024, 1, 20), d(2024, 1, 30)},
			},
			cached: []DateRange{{d(2024, 1, 5), d(2024, 1, 25)}},
			expect: []DateRange{
				{d(2024, 1, 1), d(2024, 1, 5)},
				{d(2024, 1, 25), d(2024, 1, 30)},
			},
		},
		{
			name:   "no overlap",
			needed: []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
			cached: []DateRange{{d(2024, 1, 10), d(2024, 1, 15)}},
			expect: []DateRange{{d(2024, 1, 1), d(2024, 1, 5)}},
		},
		{
			name:   "empty needed",
			needed: nil,
			cached: []DateRange{{d(2024, 1, 1), d(2024, 1, 10)}},
			expect: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SubtractRanges(tc.needed, tc.cached)
			assertRangesEqual(t, tc.expect, got)
		})
	}
}

func assertRangesEqual(t *testing.T, want, got []DateRange) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("got %d ranges, want %d\ngot:  %v\nwant: %v", len(got), len(want), fmtRanges(got), fmtRanges(want))
	}
	for i := range want {
		if !want[i].From.Equal(got[i].From) || !want[i].To.Equal(got[i].To) {
			t.Errorf("range[%d]: got [%s, %s), want [%s, %s)",
				i,
				got[i].From.Format("2006-01-02"), got[i].To.Format("2006-01-02"),
				want[i].From.Format("2006-01-02"), want[i].To.Format("2006-01-02"))
		}
	}
}

func fmtRanges(rs []DateRange) string {
	s := "["
	for i, r := range rs {
		if i > 0 {
			s += ", "
		}
		s += "[" + r.From.Format("2006-01-02") + ", " + r.To.Format("2006-01-02") + ")"
	}
	return s + "]"
}
