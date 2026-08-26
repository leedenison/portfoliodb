package postgres

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

// The migration seeds every key this build reads, so the sweep has a threshold
// before anybody has configured one -- and it is one, which is what lets a
// single-user instance promote anything at all.
func TestSettings_ThePromotionThresholdIsSeededAtOne(t *testing.T) {
	p := testDBTx(t)
	n, err := p.PromotionThreshold(context.Background())
	if err != nil {
		t.Fatalf("promotion threshold: %v", err)
	}
	if n != 1 {
		t.Errorf("promotion threshold = %d, want 1", n)
	}
}

func TestSettings_RoundTrip(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	if err := p.SetSetting(ctx, db.SettingPromotionThreshold, "3"); err != nil {
		t.Fatalf("set: %v", err)
	}
	n, err := p.PromotionThreshold(ctx)
	if err != nil {
		t.Fatalf("promotion threshold: %v", err)
	}
	if n != 3 {
		t.Errorf("promotion threshold = %d, want 3", n)
	}
	rows, err := p.ListSettings(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.Key == db.SettingPromotionThreshold {
			found = true
			if r.Value != "3" {
				t.Errorf("listed value = %q, want 3", r.Value)
			}
		}
	}
	if !found {
		t.Error("the threshold was not listed")
	}
}

// A value the sweep cannot use is an error rather than a substitution. A
// threshold of zero would promote a mapping nobody holds, and quietly reading it
// as one would be the sweep choosing a number the admin did not.
func TestSettings_AnUnusableThresholdIsRefusedRatherThanClamped(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"below one", "0"},
		{"negative", "-2"},
		{"not a number", "some"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testDBTx(t)
			ctx := context.Background()
			if err := p.SetSetting(ctx, db.SettingPromotionThreshold, tc.value); err != nil {
				t.Fatalf("set: %v", err)
			}
			if _, err := p.PromotionThreshold(ctx); err == nil {
				t.Errorf("threshold %q was accepted, want an error", tc.value)
			}
		})
	}
}

// A key with no row is a deployment that did not migrate rather than a setting
// nobody has got round to, so it reads as an error rather than as an empty
// string a caller would go on to parse.
func TestSettings_AnAbsentKeyIsAnError(t *testing.T) {
	p := testDBTx(t)
	if _, err := p.GetSetting(context.Background(), "nothing_seeds_this"); err == nil {
		t.Error("an absent key answered, want an error")
	}
}
