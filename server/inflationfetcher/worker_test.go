package inflationfetcher

import (
	"testing"
	"time"
)

func TestComputeGapRange_NoCoverage(t *testing.T) {
	end := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	from, to := computeGapRange(nil, end)
	if !from.Equal(gapStart) {
		t.Errorf("expected from=%v, got %v", gapStart, from)
	}
	if !to.Equal(end) {
		t.Errorf("expected to=%v, got %v", end, to)
	}
}

func TestComputeGapRange_FullCoverage(t *testing.T) {
	end := time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC)
	coverage := []time.Time{
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	from, to := computeGapRange(coverage, end)
	if from.Before(to) {
		t.Errorf("expected no gap, got [%v, %v)", from, to)
	}
}

func TestComputeGapRange_PartialCoverage(t *testing.T) {
	end := time.Date(2000, 6, 1, 0, 0, 0, 0, time.UTC)
	coverage := []time.Time{
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
		// gap at March, April, May
	}
	from, to := computeGapRange(coverage, end)
	expected := time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC)
	if !from.Equal(expected) {
		t.Errorf("expected gap from=%v, got %v", expected, from)
	}
	if !to.Equal(end) {
		t.Errorf("expected gap to=%v, got %v", end, to)
	}
}

func TestToDBIndices(t *testing.T) {
	indices := []MonthlyIndex{
		{Month: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), IndexValue: 130.5, BaseYear: 2015},
	}
	dbIndices := toDBIndices("GBP", "ons", indices)
	if len(dbIndices) != 1 {
		t.Fatalf("expected 1, got %d", len(dbIndices))
	}
	if dbIndices[0].Currency != "GBP" || dbIndices[0].DataProvider != "ons" {
		t.Error("unexpected field values")
	}
}
