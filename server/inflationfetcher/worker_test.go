package inflationfetcher

import (
	"context"
	"testing"
	"time"
)

// stubPlugin is a minimal Plugin implementation for worker tests.
type stubPlugin struct {
	name       string
	currencies []string
}

func (s *stubPlugin) DisplayName() string           { return s.name }
func (s *stubPlugin) SupportedCurrencies() []string { return s.currencies }
func (s *stubPlugin) DefaultConfig() []byte         { return []byte(`{}`) }
func (s *stubPlugin) FetchInflation(_ context.Context, _ []byte, _ string, _, _ time.Time) (*FetchResult, error) {
	return nil, ErrNoData
}

func TestPluginAcceptsCurrency(t *testing.T) {
	p := &stubPlugin{currencies: []string{"GBP", "EUR"}}

	if !pluginAcceptsCurrency(p, "GBP") {
		t.Error("expected GBP accepted")
	}
	if !pluginAcceptsCurrency(p, "gbp") {
		t.Error("expected case-insensitive match")
	}
	if pluginAcceptsCurrency(p, "USD") {
		t.Error("expected USD rejected")
	}
	// On the family: a plugin publishing an index for GBP publishes it for the
	// line quoted in pence. See adr/0068.
	if !pluginAcceptsCurrency(p, "GBX") {
		t.Error("expected GBX accepted by a plugin declaring GBP")
	}
}

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
