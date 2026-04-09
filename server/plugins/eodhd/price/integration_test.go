//go:build integration

package price

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

func TestIntegration_EODHD_FetchPrices(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "EODHD_API_KEY", "eodhd/price")

	tests := []struct {
		name       string
		cassette   string
		ids        []pricefetcher.Identifier
		assetClass string
		from       time.Time
		to         time.Time
		wantBars   bool
		wantPerm   bool // expect ErrPermanent (subscription limit)
	}{
		{
			name:       "fx_gbpusd_subscription_limit",
			cassette:   "testdata/cassettes/fx_gbpusd_sub_limit",
			ids:        []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBPUSD"}},
			assetClass: db.AssetClassFX,
			from:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			to:         time.Date(2025, 4, 7, 0, 0, 0, 0, time.UTC),
			wantPerm:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll, "eodhd/price")

			p := NewPlugin(nil, nil, httpClient, nil)
			cfg, err := json.Marshal(configJSON{EODHDAPIKey: apiKey})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			result, err := p.FetchPrices(context.Background(), cfg, tc.ids, tc.assetClass, tc.from, tc.to)

			if tc.wantPerm {
				var permErr *pricefetcher.ErrPermanent
				if !errors.As(err, &permErr) {
					t.Fatalf("expected ErrPermanent, got err=%v result=%+v", err, result)
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
