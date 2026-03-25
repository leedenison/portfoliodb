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

func TestIntegration_OpenFIGI_Identify(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "OPENFIGI_API_KEY")

	tests := []struct {
		name      string
		cassette  string
		hints     identifier.Hints
		idHints   []identifier.Identifier
		wantClass string // expected AssetClass, empty means ErrNotIdentified
		wantErr   error
	}{
		{
			name:     "stock_ibm_ticker",
			cassette: "testdata/cassettes/stock_ibm_ticker",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "TICKER", Value: "IBM"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "stock_aapl_isin",
			cassette: "testdata/cassettes/stock_aapl_isin",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "ISIN", Value: "US0378331005"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "option_aapl_occ",
			cassette: "testdata/cassettes/option_aapl_occ",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption},
			idHints: []identifier.Identifier{
				{Type: "OCC", Value: "AAPL251219C00200000"},
			},
			wantClass: "OPTION",
		},
		{
			name:     "not_found",
			cassette: "testdata/cassettes/not_found",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "TICKER", Value: "ZZZZNOTREAL"},
			},
			wantErr: identifier.ErrNotIdentified,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient)
			cfg, err := json.Marshal(configJSON{
				OpenFIGIAPIKey: apiKey,
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
			if tc.wantClass == "OPTION" && len(inst.UnderlyingIdentifiers) == 0 {
				t.Error("expected UnderlyingIdentifiers for option")
			}
		})
	}
}
