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

func TestIsWholeForwardSplit(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{"1", "2", true},
		{"1", "10", true},
		{"1", "4", true},
		{"2", "1", false}, // reverse
		{"2", "3", false}, // non-whole
		{"1", "1", false}, // no change
		{"0", "2", false}, // invalid
		{"", "2", false},  // invalid
		{"1", "", false},  // invalid
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
	if got.Strike.String() != "230" {
		t.Errorf("strike = %v, want 230", got.Strike)
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
// a split occurred after hintsValidAt and while the contract was still listed,
// the OCC strike is adjusted.
func TestAdjustOCCForKnownSplits_SplitAfterHintsValidAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL260116C00200000"}, // compact OCC, $200 strike
	}
	validAt := d(2025, 1, 1)           // before split
	timer := fixedTimer(d(2025, 7, 1)) // after split, before expiry

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Type != "OCC" {
		t.Errorf("adjusted[0].Type = %q, want OCC", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL260116C00100000" {
		t.Errorf("adjusted OCC = %q, want AAPL260116C00100000", adjusted[0].Value)
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

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC should not change, got %q", adjusted[0].Value)
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

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Value != "AAPL250117C00400000" {
		t.Errorf("OCC should not change for future split, got %q", adjusted[0].Value)
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

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, nil, nil)

	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC should not change when hintsValidAt nil, got %q", adjusted[0].Value)
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

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, nil)

	if adjusted[0].Value != "AAPL" {
		t.Errorf("non-OCC hint should not change, got %q", adjusted[0].Value)
	}
}

// TestAdjustOCCForKnownSplits_Vintage covers the market time the returned
// hints reflect, which is what the caller dates its names from. A rebased
// hint reflects now; one left alone still reflects its own vintage, and a split
// learned of later must find it that way or the retroactive option adjustment is
// skipped for a symbol that never had it.
//
// The rebased case uses an expired contract, which is carried only to its expiry
// and still reports now: it will not be restated again, so that is as current as
// its identity gets.
func TestAdjustOCCForKnownSplits_Vintage(t *testing.T) {
	validAt := d(2024, 6, 15)
	occ := []identifier.Identifier{{Type: "OCC", Value: "AAPL250117C00760000"}}

	tests := []struct {
		name   string
		hints  []identifier.Identifier
		splits []db.StockSplit
		ticker string
		want   *time.Time
	}{
		{
			name:   "rebased as far as it can go reflects now",
			hints:  occ,
			ticker: "AAPL",
			splits: []db.StockSplit{{ExDate: d(2024, 8, 1), SplitFrom: "1", SplitTo: "4"}},
			want:   nil,
		},
		{
			name:   "split unknown leaves the hint at its own vintage",
			hints:  occ,
			ticker: "AAPL",
			splits: nil,
			want:   &validAt,
		},
		{
			name:   "split already reflected in the hint leaves its vintage",
			hints:  occ,
			ticker: "AAPL",
			splits: []db.StockSplit{{ExDate: d(2024, 1, 2), SplitFrom: "1", SplitTo: "4"}},
			want:   &validAt,
		},
		{
			name:  "no OCC hint reflects now",
			hints: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDB := mock.NewMockDB(ctrl)
			if tc.ticker != "" {
				mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), tc.ticker).Return(tc.splits, nil)
			}

			_, got := AdjustOCCForKnownSplits(context.Background(), mockDB, tc.hints, &validAt, fixedTimer(d(2026, 7, 31)))

			switch {
			case tc.want == nil && got != nil:
				t.Errorf("vintage = %v, want nil (reflects now)", got)
			case tc.want != nil && got == nil:
				t.Errorf("vintage = nil, want %v", *tc.want)
			case tc.want != nil && !got.Equal(*tc.want):
				t.Errorf("vintage = %v, want %v", *got, *tc.want)
			}
		})
	}
}

func TestSplitFactorBetween(t *testing.T) {
	splits := []db.StockSplit{
		{ExDate: d(2024, 3, 1), SplitFrom: "1", SplitTo: "2"},
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "5"},
	}
	tests := []struct {
		name             string
		since, until     time.Time
		wantNum, wantDen string
	}{
		{"both included", d(2024, 1, 1), d(2026, 1, 1), "10", "1"},
		{"only first", d(2024, 1, 1), d(2025, 1, 1), "2", "1"},
		{"only second", d(2024, 6, 1), d(2026, 1, 1), "5", "1"},
		{"none (too early)", d(2023, 1, 1), d(2024, 1, 1), "1", "1"},
		{"none (too late)", d(2026, 1, 1), d(2027, 1, 1), "1", "1"},
		{"until equals ex_date (inclusive)", d(2024, 1, 1), d(2024, 3, 1), "2", "1"},
		{"since equals ex_date (exclusive)", d(2024, 3, 1), d(2025, 1, 1), "1", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, den := splitFactorBetween(splits, tt.since, tt.until)
			if num.String() != tt.wantNum || den.String() != tt.wantDen {
				t.Errorf("got %v/%v, want %v/%v", num, den, tt.wantNum, tt.wantDen)
			}
		})
	}
}

// TestAdjustOCC_PostExpirySplitLeavesHint is the identification half of issue
// 0058: a split effective after the contract expired never restated it, so the
// hint keeps its original strike. Carrying it forward would name a contract that
// never traded, and would miss the stored row -- which the pending-split pass
// also leaves alone -- creating a duplicate instrument.
func TestAdjustOCC_PostExpirySplitLeavesHint(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	// Option expires 2025-01-17, split on 2025-06-01 (after expiry).
	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250117C00200000"}, // $200 strike
	}
	validAt := d(2024, 6, 1)           // before split
	timer := fixedTimer(d(2025, 7, 1)) // after split

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Type != "OCC" {
		t.Errorf("[0].Type = %q, want OCC", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC = %q, want AAPL250117C00200000 (original strike)", adjusted[0].Value)
	}
}

// TestAdjustOCC_RebasesOnlySplitsBeforeExpiry covers the expiry bound where it
// bites hardest: one split took effect while the contract was listed and a later
// one did not, so only the first is folded into the hint.
func TestAdjustOCC_RebasesOnlySplitsBeforeExpiry(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	// Option expires 2025-06-17.
	// Split 1: 2025-03-01 (before expiry) 2:1
	// Split 2: 2025-09-01 (after expiry) 5:1
	splits := []db.StockSplit{
		{ExDate: d(2025, 3, 1), SplitFrom: "1", SplitTo: "2"},
		{ExDate: d(2025, 9, 1), SplitFrom: "1", SplitTo: "5"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL250617C01000000"}, // $1000 strike, expires 2025-06-17
	}
	validAt := d(2024, 6, 1)
	timer := fixedTimer(d(2025, 12, 1))

	adjusted, _ := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Type != "OCC" {
		t.Errorf("[0].Type = %q, want OCC", adjusted[0].Type)
	}
	// Only the pre-expiry split (2:1) applied. $1000/2 = $500.
	if adjusted[0].Value != "AAPL250617C00500000" {
		t.Errorf("OCC = %q, want AAPL250617C00500000", adjusted[0].Value)
	}
}
