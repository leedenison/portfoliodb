//go:build integration

package corporateevents

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

// TestIntegration_EODHD_FetchEvents records cassettes for the EODHD splits
// and dividends endpoints. Run with:
//
//	make integration-test-record
//
// or directly:
//
//	VCR_MODE=record EODHD_API_KEY=... go test -tags integration -count=1 \
//	    ./server/plugins/eodhd/corporateevents/...
//
// All test windows are within the last 12 months so the EODHD free tier
// covers them. AAPL pays a quarterly dividend so any 12-month window
// catches at least three ex-divs; AAPL has not split since 2020 so the
// splits response is expected to be empty -- that path is also worth
// pinning, since the empty-success case drives coverage recording in
// the worker. To exercise the multi-split parse path against a real
// recent split, add a subtest with the relevant ticker and ex_date.
func TestIntegration_EODHD_FetchEvents(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "EODHD_API_KEY")

	tests := []struct {
		name         string
		cassette     string
		ids          []corporateevents.Identifier
		assetClass   string
		from         time.Time
		to           time.Time
		wantSplits   int  // exact split count expected; 0 = empty splits OK
		wantDividend bool // require at least one dividend in the response
	}{
		{
			// One-month window around AAPL's November 2025 ex-dividend.
			// Tight enough that the cassette stays small but wide enough
			// to absorb minor calendar shifts.
			name:         "aapl_dividend_nov_2025",
			cassette:     "testdata/cassettes/aapl_dividend_nov_2025",
			ids:          []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
			assetClass:   db.AssetClassStock,
			from:         time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC),
			to:           time.Date(2025, 11, 30, 0, 0, 0, 0, time.UTC),
			wantDividend: true,
		},
		{
			// Twelve-month window for AAPL. Catches every quarterly
			// ex-dividend in the year and confirms the splits response
			// is an empty array (AAPL has not split since 2020).
			name:         "aapl_last_12_months",
			cassette:     "testdata/cassettes/aapl_last_12_months",
			ids:          []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
			assetClass:   db.AssetClassStock,
			from:         time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
			to:           time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantSplits:   0,
			wantDividend: true,
		},
		{
			// NFLX split inside this window. Pins the multi-split parse
			// path against a real recent split, complementing the empty-
			// splits AAPL case above. NFLX does not pay a regular cash
			// dividend, so the dividends response is the empty array.
			name:       "nflx_split_last_12_months",
			cassette:   "testdata/cassettes/nflx_split_last_12_months",
			ids:        []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "NFLX"}},
			assetClass: db.AssetClassStock,
			from:       time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantSplits: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient, exchangemap.New())
			cfg, err := json.Marshal(configJSON{EODHDAPIKey: apiKey})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			got, err := p.FetchEvents(context.Background(), cfg, tc.ids, tc.assetClass, tc.from, tc.to)
			if err != nil {
				t.Fatalf("FetchEvents: %v", err)
			}
			if got == nil {
				t.Fatal("FetchEvents returned nil result with no error")
			}

			if len(got.Splits) != tc.wantSplits {
				t.Errorf("got %d splits, want %d: %+v", len(got.Splits), tc.wantSplits, got.Splits)
			}
			for _, s := range got.Splits {
				if s.SplitFrom == "" || s.SplitTo == "" || s.ExDate.IsZero() {
					t.Errorf("split missing required fields: %+v", s)
				}
			}

			if tc.wantDividend {
				if len(got.CashDividends) == 0 {
					t.Error("expected at least one dividend, got 0")
				}
				for _, d := range got.CashDividends {
					if d.Amount == "" || d.Currency == "" || d.ExDate.IsZero() {
						t.Errorf("dividend missing required fields: %+v", d)
					}
				}
			}
		})
	}
}
