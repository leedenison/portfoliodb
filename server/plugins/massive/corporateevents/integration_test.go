//go:build integration

package corporateevents

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

// TestIntegration_Massive_FetchEvents records cassettes for the Massive
// corporate-actions endpoints. Run with:
//
//	make integration-test-record
//
// or directly:
//
//	VCR_MODE=record MASSIVE_API_KEY=... go test -tags integration -count=1 \
//	    ./server/plugins/massive/corporateevents/...
//
// All test windows are within the last 12 months so the Massive free tier
// covers them. AAPL pays a quarterly dividend so any 12-month window
// catches at least three ex-divs; AAPL has not split since 2020 so the
// splits response is expected to be empty -- that path is also worth
// pinning, since the empty-success case drives coverage recording in
// the worker. To exercise the multi-split parse path against a real
// recent split, add a subtest with the relevant ticker and ex_date.
func TestIntegration_Massive_FetchEvents(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "MASSIVE_API_KEY")

	tests := []struct {
		name         string
		cassette     string
		ids          []corporateevents.Identifier
		assetClass   string
		from         time.Time
		to           time.Time
		wantSplits   int
		wantDividend bool
	}{
		{
			name:         "aapl_dividend_nov_2025",
			cassette:     "testdata/cassettes/aapl_dividend_nov_2025",
			ids:          []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
			assetClass:   db.AssetClassStock,
			from:         time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC),
			to:           time.Date(2025, 11, 30, 0, 0, 0, 0, time.UTC),
			wantDividend: true,
		},
		{
			// Twelve-month window for AAPL. Catches every quarterly
			// ex-dividend in the year and confirms the splits response
			// is an empty array (AAPL has not split since 2020). Also
			// exercises pagination if Massive returns more than one
			// page for the dividend window (4 quarterly dividends fits
			// in one page at default limit).
			name:         "aapl_last_12_months",
			cassette:     "testdata/cassettes/aapl_last_12_months",
			ids:          []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
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
			ids:        []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "NFLX"}},
			assetClass: db.AssetClassStock,
			from:       time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantSplits: 1,
		},
	}

	// Massive's free tier caps at 5 requests/minute. The three subtests
	// fire 6 calls (splits + dividends per subtest), so without rate
	// limiting the recording run blows past the cap on the last call.
	// Enable the client's per-minute limiter only in record mode -- in
	// replay mode the cassette responses are instant and we do not want
	// to pay the spacing cost on every CI run.
	var massiveCallsPerMin *int
	if vcr.IsRecording() {
		n := 4 // headroom under the 5/min free-tier cap
		massiveCallsPerMin = &n
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient)
			cfg, err := json.Marshal(configJSON{
				MassiveAPIKey: apiKey,
				CallsPerMin:   massiveCallsPerMin,
			})
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
