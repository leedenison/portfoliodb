//go:build integration

package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

func TestIntegration_EODHD_Identify(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "EODHD_API_KEY")

	tests := []struct {
		name      string
		cassette  string
		hints     identifier.Hints
		idHints   []identifier.Identifier
		wantClass string // expected AssetClass, empty means ErrNotIdentified
		wantErr   error
		wantCUSIP bool // when true, assert CUSIP identifier is present
	}{
		{
			name:     "stock_aapl",
			cassette: "testdata/cassettes/stock_aapl",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "stock_brk_b",
			cassette: "testdata/cassettes/stock_brk_b",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "MIC_TICKER", Value: "BRK-B"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "stock_isin_lookup",
			cassette: "testdata/cassettes/stock_isin_lookup",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "ISIN", Value: "US0378331005"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "stock_exchange_hint",
			cassette: "testdata/cassettes/stock_exchange_hint",
			hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "not_found",
			cassette: "testdata/cassettes/not_found",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "MIC_TICKER", Value: "ZZZZNOTREAL"},
			},
			wantErr: identifier.ErrNotIdentified,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient, nil)
			cfg, err := json.Marshal(configJSON{
				EODHDAPIKey: apiKey,
			})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			inst, ids, err := p.Identify(
				context.Background(),
				cfg,
				"test-broker",
				"test-source",
				"test-description",
				tc.hints,
				tc.idHints,
			)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if inst == nil {
				t.Fatal("expected instrument, got nil")
			}
			if inst.AssetClass != tc.wantClass {
				t.Errorf("AssetClass = %q, want %q", inst.AssetClass, tc.wantClass)
			}
			if len(ids) == 0 {
				t.Error("expected at least one identifier")
			}
			if tc.wantCUSIP {
				found := false
				for _, id := range ids {
					if id.Type == "CUSIP" && id.Value != "" {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected CUSIP identifier")
				}
			}
		})
	}
}
