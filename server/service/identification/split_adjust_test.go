package identification

import (
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
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
			got := splitFactorSince(splits, tt.since)
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
	got := splitFactorSince(splits, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
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
