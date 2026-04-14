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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	// Option expires 2025-01-17, split is 2025-06-01 (after expiry).
	// OCC_AT_EXPIRY should have original strike (no post-hintsValidAt
	// splits before expiry), OCC should be adjusted.
	if len(adjusted) != 2 {
		t.Fatalf("want 2 hints (OCC_AT_EXPIRY + OCC), got %d", len(adjusted))
	}
	if adjusted[0].Type != identifier.InternalHintTypeOCCAtExpiry {
		t.Errorf("adjusted[0].Type = %q, want OCC_AT_EXPIRY", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC_AT_EXPIRY = %q, want AAPL250117C00200000 (original strike)", adjusted[0].Value)
	}
	if adjusted[1].Type != "OCC" {
		t.Errorf("adjusted[1].Type = %q, want OCC", adjusted[1].Type)
	}
	if adjusted[1].Value != "AAPL250117C00100000" {
		t.Errorf("adjusted OCC = %q, want AAPL250117C00100000", adjusted[1].Value)
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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	// No split adjustment (split before hintsValidAt), but OCC_AT_EXPIRY
	// is still emitted for the expired option.
	if len(adjusted) != 2 {
		t.Fatalf("want 2 hints, got %d", len(adjusted))
	}
	if adjusted[0].Type != identifier.InternalHintTypeOCCAtExpiry {
		t.Errorf("adjusted[0].Type = %q, want OCC_AT_EXPIRY", adjusted[0].Type)
	}
	if adjusted[1].Value != "AAPL250117C00200000" {
		t.Errorf("OCC should not change, got %q", adjusted[1].Value)
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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	// Option expired (Jan 17 < Jun 1), so OCC_AT_EXPIRY is emitted.
	// Split is after now, so neither OCC nor OCC_AT_EXPIRY is adjusted.
	if len(adjusted) != 2 {
		t.Fatalf("want 2 hints, got %d", len(adjusted))
	}
	if adjusted[0].Type != identifier.InternalHintTypeOCCAtExpiry {
		t.Errorf("adjusted[0].Type = %q, want OCC_AT_EXPIRY", adjusted[0].Type)
	}
	if adjusted[1].Value != "AAPL250117C00400000" {
		t.Errorf("OCC should not change for future split, got %q", adjusted[1].Value)
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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, nil, nil)

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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, nil)

	if adjusted[0].Value != "AAPL" {
		t.Errorf("non-OCC hint should not change, got %q", adjusted[0].Value)
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

func TestSplitFactorBetween(t *testing.T) {
	splits := []db.StockSplit{
		{ExDate: d(2024, 3, 1), SplitFrom: "1", SplitTo: "2"},
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "5"},
	}
	tests := []struct {
		name         string
		since, until time.Time
		want         float64
	}{
		{"both included", d(2024, 1, 1), d(2026, 1, 1), 10.0},
		{"only first", d(2024, 1, 1), d(2025, 1, 1), 2.0},
		{"only second", d(2024, 6, 1), d(2026, 1, 1), 5.0},
		{"none (too early)", d(2023, 1, 1), d(2024, 1, 1), 1.0},
		{"none (too late)", d(2026, 1, 1), d(2027, 1, 1), 1.0},
		{"until equals ex_date (inclusive)", d(2024, 1, 1), d(2024, 3, 1), 2.0},
		{"since equals ex_date (exclusive)", d(2024, 3, 1), d(2025, 1, 1), 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFactorBetween(splits, tt.since, tt.until)
			if got != tt.want {
				t.Errorf("got %f, want %f", got, tt.want)
			}
		})
	}
}

// TestAdjustOCC_OCCAtExpiry_PostExpirySplit verifies that when a split
// occurs after the option's expiry, OCC_AT_EXPIRY has the original
// strike while the OCC hint is adjusted.
func TestAdjustOCC_OCCAtExpiry_PostExpirySplit(t *testing.T) {
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
	validAt := d(2024, 6, 1) // before split
	timer := fixedTimer(d(2025, 7, 1)) // after split

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 2 {
		t.Fatalf("want 2 hints, got %d: %+v", len(adjusted), adjusted)
	}
	// OCC_AT_EXPIRY: no splits between validAt and expiry, original strike.
	if adjusted[0].Type != identifier.InternalHintTypeOCCAtExpiry {
		t.Errorf("[0].Type = %q, want OCC_AT_EXPIRY", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL250117C00200000" {
		t.Errorf("OCC_AT_EXPIRY = %q, want AAPL250117C00200000", adjusted[0].Value)
	}
	// OCC: split applied, $100 strike.
	if adjusted[1].Value != "AAPL250117C00100000" {
		t.Errorf("OCC = %q, want AAPL250117C00100000", adjusted[1].Value)
	}
}

// TestAdjustOCC_OCCAtExpiry_PreExpirySplit verifies that when a split
// occurs before the option's expiry, both OCC and OCC_AT_EXPIRY have
// the adjusted strike.
func TestAdjustOCC_OCCAtExpiry_PreExpirySplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	// Option expires 2026-01-17, split on 2025-06-01 (before expiry).
	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL260117C00200000"}, // $200 strike, expires 2026-01-17
	}
	validAt := d(2024, 6, 1)
	timer := fixedTimer(d(2025, 7, 1))

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	// Option hasn't expired (2026-01-17 > 2025-07-01), so no OCC_AT_EXPIRY.
	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint (no OCC_AT_EXPIRY for non-expired), got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Type != "OCC" {
		t.Errorf("[0].Type = %q, want OCC", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL260117C00100000" {
		t.Errorf("OCC = %q, want AAPL260117C00100000", adjusted[0].Value)
	}
}

// TestAdjustOCC_OCCAtExpiry_MultipleSplits verifies correct handling
// when one split is before expiry and another is after.
func TestAdjustOCC_OCCAtExpiry_MultipleSplits(t *testing.T) {
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

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	if len(adjusted) != 2 {
		t.Fatalf("want 2 hints, got %d: %+v", len(adjusted), adjusted)
	}
	// OCC_AT_EXPIRY: only pre-expiry split (2:1) applied. $1000/2 = $500.
	if adjusted[0].Type != identifier.InternalHintTypeOCCAtExpiry {
		t.Errorf("[0].Type = %q, want OCC_AT_EXPIRY", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL250617C00500000" {
		t.Errorf("OCC_AT_EXPIRY = %q, want AAPL250617C00500000", adjusted[0].Value)
	}
	// OCC: both splits applied. $1000/10 = $100.
	if adjusted[1].Value != "AAPL250617C00100000" {
		t.Errorf("OCC = %q, want AAPL250617C00100000", adjusted[1].Value)
	}
}

// TestAdjustOCC_OCCAtExpiry_NotExpired verifies that OCC_AT_EXPIRY is
// not emitted for options that have not yet expired.
func TestAdjustOCC_OCCAtExpiry_NotExpired(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	splits := []db.StockSplit{
		{ExDate: d(2025, 6, 1), SplitFrom: "1", SplitTo: "2"},
	}
	mockDB.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").Return(splits, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "AAPL261219C00200000"}, // expires 2026-12-19
	}
	validAt := d(2024, 6, 1)
	timer := fixedTimer(d(2025, 7, 1)) // option not expired yet

	adjusted := AdjustOCCForKnownSplits(ctx, mockDB, hints, &validAt, timer)

	// No OCC_AT_EXPIRY for non-expired options.
	if len(adjusted) != 1 {
		t.Fatalf("want 1 hint, got %d: %+v", len(adjusted), adjusted)
	}
	if adjusted[0].Type != "OCC" {
		t.Errorf("[0].Type = %q, want OCC", adjusted[0].Type)
	}
	if adjusted[0].Value != "AAPL261219C00100000" {
		t.Errorf("OCC = %q, want AAPL261219C00100000 (split applied)", adjusted[0].Value)
	}
}
