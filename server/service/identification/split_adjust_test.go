package identification

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/clock"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

func TestSplitFactorSince(t *testing.T) {
	splits := []db.StockSplit{
		{ExDate: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC), SplitFrom: "1", SplitTo: "2"},
		{ExDate: time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC), SplitFrom: "1", SplitTo: "10"},
	}
	tests := []struct {
		name  string
		since time.Time
		want  float64
	}{
		{
			name:  "before all splits",
			since: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
			want:  20.0, // 2 * 10
		},
		{
			name:  "between splits",
			since: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			want:  10.0, // only the 10:1
		},
		{
			name:  "after all splits",
			since: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			want:  1.0, // no applicable splits
		},
		{
			name:  "on split date (not after)",
			since: time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC),
			want:  1.0, // ex_date must be strictly after since
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFactorSince(splits, tt.since, nil)
			if got != tt.want {
				t.Errorf("got %f, want %f", got, tt.want)
			}
		})
	}
}

func TestSplitFactorSince_FutureSplitIgnored(t *testing.T) {
	splits := []db.StockSplit{
		{ExDate: time.Now().AddDate(1, 0, 0), SplitFrom: "1", SplitTo: "4"},
	}
	got := splitFactorSince(splits, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), nil)
	if got != 1.0 {
		t.Errorf("got %f, want 1.0 (future split should be ignored)", got)
	}
}

func TestIsWholeForwardSplit(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{"1", "2", true},
		{"1", "10", true},
		{"1", "4", true},
		{"2", "1", false},  // reverse
		{"2", "3", false},  // non-whole
		{"1", "1", false},  // no change
		{"0", "2", false},  // invalid
		{"", "2", false},   // invalid
		{"1", "", false},   // invalid
	}
	for _, tt := range tests {
		t.Run(tt.from+":"+tt.to, func(t *testing.T) {
			got := IsWholeForwardSplit(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("IsWholeForwardSplit(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestOptionFieldsFromIdentifiers(t *testing.T) {
	ids := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
		{Type: "OCC", Value: "AAPL251219C00230000"},
	}
	got := optionFieldsFromIdentifiers(ids)
	if got == nil {
		t.Fatal("expected non-nil OptionFields")
	}
	if got.Strike != 230 {
		t.Errorf("strike = %f, want 230", got.Strike)
	}
	if got.PutCall != "C" {
		t.Errorf("put_call = %q, want C", got.PutCall)
	}
	if got.Expiry.IsZero() {
		t.Error("expiry is zero")
	}
}

func TestOptionFieldsFromIdentifiers_NoOCC(t *testing.T) {
	ids := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
	}
	got := optionFieldsFromIdentifiers(ids)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func fixedTimer(t time.Time) *clock.Timer {
	return &clock.Timer{NowFunc: func() time.Time { return t }}
}

// TestAdjustOCCForKnownSplits_SplitAfterHintsValidAt verifies that when
// a split occurred after hintsValidAt, the OCC strike is adjusted.
func TestAdjustOCCForKnownSplits_SplitAfterHintsValidAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250117C00200000"}, // compact OCC, $200 strike
	}
	validAt := d(2025, 1, 1) // before split
	timer := fixedTimer(d(2025, 7, 1)) // after split

	adjusted, appliedSplits := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if adjusted[0].Value != "AAPL250117C00100000" {
		t.Errorf("adjusted OCC = %q, want AAPL250117C00100000", adjusted[0].Value)
	}
	if len(appliedSplits) != 1 {
		t.Fatalf("applied splits = %d, want 1", len(appliedSplits))
	}
	if appliedSplits[0].SplitTo != "2" {
		t.Errorf("applied split_to = %q, want 2", appliedSplits[0].SplitTo)
	}
}

// TestAdjustOCCForKnownSplits_SplitBeforeHintsValidAt verifies that
// splits with ex_date before hintsValidAt do not adjust the OCC.
func TestAdjustOCCForKnownSplits_SplitBeforeHintsValidAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	splits := []db.StockSplit{
		{ExDate: d(2024, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250117C00200000"},
	}
	validAt := d(2025, 1, 1) // after split ex_date
	timer := fixedTimer(d(2025, 7, 1))

	adjusted, appliedSplits := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC should not change, got %q", adjusted[0].Value)
	}
	if len(appliedSplits) != 0 {
		t.Errorf("applied splits = %d, want 0", len(appliedSplits))
	}
}

// TestAdjustOCCForKnownSplits_FutureSplit verifies that splits with
// ex_date after Timer.Now() do not adjust the OCC.
func TestAdjustOCCForKnownSplits_FutureSplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	splits := []db.StockSplit{
		{ExDate: d(2025, 12, 1), SplitFrom: "1", SplitTo: "4"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250117C00400000"},
	}
	validAt := d(2025, 1, 1)
	timer := fixedTimer(d(2025, 6, 1)) // before split ex_date

	adjusted, appliedSplits := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if adjusted[0].Value != "AAPL250117C00400000" {
		t.Errorf("OCC should not change for future split, got %q", adjusted[0].Value)
	}
	if len(appliedSplits) != 0 {
		t.Errorf("applied splits = %d, want 0", len(appliedSplits))
	}
}

// TestAdjustOCCForKnownSplits_NilHintsValidAt verifies that when
// hintsValidAt is nil, hints are returned unchanged.
func TestAdjustOCCForKnownSplits_NilHintsValidAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()
	// No DB calls expected.
	_ = mockDB

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250117C00200000"},
	}

	adjusted, appliedSplits := AdjustOCCForKnownSplits(ctx, mockDB, hints, nil, nil)

	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC should not change when hintsValidAt nil, got %q", adjusted[0].Value)
	}
	if len(appliedSplits) != 0 {
		t.Errorf("applied splits = %d, want 0", len(appliedSplits))
	}
}

// TestAdjustOCCForKnownSplits_NonOCCHintUnchanged verifies that non-OCC
// hints pass through unmodified.
func TestAdjustOCCForKnownSplits_NonOCCHintUnchanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()
	// No DB calls expected for non-OCC hints.

	hints := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
	}
	validAt := d(2025, 1, 1)

	adjusted, appliedSplits := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, nil)

	if adjusted[0].Value != "AAPL" {
		t.Errorf("non-OCC hint should not change, got %q", adjusted[0].Value)
	}
	if len(appliedSplits) != 0 {
		t.Errorf("applied splits = %d, want 0", len(appliedSplits))
	}
}

// TestSplitFactorSince_WithTimer verifies that splitFactorSince respects
// the Timer for the "now" boundary.
func TestSplitFactorSince_WithTimer(t *testing.T) {
	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "4"},
	}

	// Timer before ex_date: split not applicable.
	timer := fixedTimer(d(2025, 3, 1))
	got := splitFactorSince(splits, d(2024, 1, 1), timer)
	if got != 1.0 {
		t.Errorf("with timer before ex_date: got %f, want 1.0", got)
	}

	// Timer after ex_date: split applicable.
	timer = fixedTimer(d(2025, 7, 1))
	got = splitFactorSince(splits, d(2024, 1, 1), timer)
	if got != 4.0 {
		t.Errorf("with timer after ex_date: got %f, want 4.0", got)
	}
}
