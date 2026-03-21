package db

import (
	"sort"
	"time"
)

// Day is a convenience for adding calendar days to a time.Time.
const Day = 24 * time.Hour

// MergeRanges merges overlapping or adjacent half-open [From, To) ranges.
// Input need not be sorted. Returns a sorted, non-overlapping slice.
func MergeRanges(ranges []DateRange) []DateRange {
	if len(ranges) <= 1 {
		return ranges
	}
	sorted := make([]DateRange, len(ranges))
	copy(sorted, ranges)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].From.Before(sorted[j].From)
	})
	merged := []DateRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if !r.From.After(last.To) {
			if r.To.After(last.To) {
				last.To = r.To
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// BridgeRanges merges ranges separated by gaps of maxGap or fewer calendar days.
// This bridges weekend/holiday gaps in price coverage so they are not treated as
// missing data. Input must be sorted and non-overlapping (as returned by MergeRanges).
func BridgeRanges(ranges []DateRange, maxGap int) []DateRange {
	if len(ranges) <= 1 {
		return ranges
	}
	bridgeDur := time.Duration(maxGap) * Day
	merged := []DateRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if !r.From.After(last.To.Add(bridgeDur)) {
			if r.To.After(last.To) {
				last.To = r.To
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// SubtractRanges computes needed minus cached: the portions of needed ranges
// not covered by any cached range. Both inputs should be sorted and
// non-overlapping (as returned by MergeRanges). Returns sorted, non-overlapping
// gap ranges.
func SubtractRanges(needed, cached []DateRange) []DateRange {
	if len(cached) == 0 {
		return needed
	}
	var gaps []DateRange
	ci := 0
	for _, n := range needed {
		cur := n.From
		for ci < len(cached) && cached[ci].To.Before(n.From) || ci < len(cached) && cached[ci].To.Equal(n.From) {
			ci++
		}
		for j := ci; j < len(cached) && cached[j].From.Before(n.To); j++ {
			c := cached[j]
			if c.From.After(cur) {
				end := n.To
				if c.From.Before(end) {
					end = c.From
				}
				gaps = append(gaps, DateRange{From: cur, To: end})
			}
			if c.To.After(cur) {
				cur = c.To
			}
		}
		if cur.Before(n.To) {
			gaps = append(gaps, DateRange{From: cur, To: n.To})
		}
	}
	return gaps
}
