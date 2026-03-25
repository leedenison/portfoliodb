//go:build integration

package price

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

func TestIntegration_Massive_FetchPrices_FX(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "MASSIVE_API_KEY")

	tests := []struct {
		name       string
		cassette   string
		ids        []pricefetcher.Identifier
		assetClass string
		from       time.Time
		to         time.Time
		wantBars   bool // true = expect at least one bar
		wantErr    bool
	}{
		{
			name:       "fx_eurusd",
			cassette:   "testdata/cassettes/fx_eurusd",
			ids:        []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "EURUSD"}},
			assetClass: db.AssetClassFX,
			from:       time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC),
			wantBars:   true,
		},
		{
			name:       "fx_gbpusd",
			cassette:   "testdata/cassettes/fx_gbpusd",
			ids:        []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBPUSD"}},
			assetClass: db.AssetClassFX,
			from:       time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC),
			wantBars:   true,
		},
		{
			name:       "fx_stock_for_comparison",
			cassette:   "testdata/cassettes/fx_stock_aapl",
			ids:        []pricefetcher.Identifier{{Type: "TICKER", Value: "AAPL"}},
			assetClass: db.AssetClassStock,
			from:       time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2025, 1, 4, 0, 0, 0, 0, time.UTC),
			wantBars:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient)
			cfg, err := json.Marshal(configJSON{
				MassiveAPIKey: apiKey,
			})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			result, err := p.FetchPrices(context.Background(), cfg, tc.ids, tc.assetClass, tc.from, tc.to)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchPrices: %v", err)
			}
			if tc.wantBars && len(result.Bars) == 0 {
				t.Fatal("expected at least one bar, got 0")
			}
			for _, bar := range result.Bars {
				if bar.Close <= 0 {
					t.Errorf("bar %v: close=%v, want >0", bar.Date.Format("2006-01-02"), bar.Close)
				}
			}
		})
	}
}
