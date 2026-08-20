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
	apiKey := vcr.EnvOrSkip(t, "OPENFIGI_API_KEY", "openfigi/identifier")

	tests := []struct {
		name      string
		cassette  string
		hints     identifier.Hints
		idHints   []identifier.Identifier
		wantClass string // expected AssetClass, empty means ErrNotIdentified
		// wantCurrency is what the plugin must have recorded on the instrument.
		wantCurrency string
		wantErr      error
	}{
		{
			name:     "stock_ibm_ticker",
			cassette: "testdata/cassettes/stock_ibm_ticker",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "MIC_TICKER", Value: "IBM"},
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
		// The two cases below pin a provider behaviour the resolver depends on:
		// Mapping filters on the currency it is given rather than ignoring it.
		//
		// That is what lets the plugin record the currency on the instrument
		// without echoing an unverified hint, and what lets the resolver count a
		// matching currency as evidence that a guessed identifier found the right
		// security (adr/0059). If OpenFIGI ever became permissive here, the
		// excluded case below would start returning results, and the check two
		// layers up would go quietly vacuous rather than failing. This is the test
		// that would notice.
		{
			name:     "currency_filter_confirms",
			cassette: "testdata/cassettes/currency_filter_confirms",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock, Currency: "USD"},
			idHints: []identifier.Identifier{
				{Type: "ISIN", Value: "US0378331005"},
			},
			wantClass:    "STOCK",
			wantCurrency: "USD",
		},
		{
			name:     "currency_filter_excludes",
			cassette: "testdata/cassettes/currency_filter_excludes",
			// Apple has no JPY listing. JPY is a currency OpenFIGI filters by
			// perfectly well -- a Japanese security matches it -- so an empty
			// answer here is the filter working, not the code being rejected.
			hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock, Currency: "JPY"},
			idHints: []identifier.Identifier{
				{Type: "ISIN", Value: "US0378331005"},
			},
			wantErr: identifier.ErrNotIdentified,
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
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll, "openfigi/identifier")

			p := NewPlugin(nil, httpClient, nil)
			cfg, err := json.Marshal(configJSON{
				OpenFIGIAPIKey: apiKey,
			})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			res, err := p.Identify(context.Background(), cfg, "test-broker", "test-source", "test-description", identifier.Identity{Stated: tc.idHints, Hints: tc.hints})

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Instrument == nil {
				t.Fatal("expected instrument, got nil")
			}
			if res.Instrument.AssetClass != tc.wantClass {
				t.Errorf("AssetClass = %q, want %q", res.Instrument.AssetClass, tc.wantClass)
			}
			if res.Instrument.Currency != tc.wantCurrency {
				t.Errorf("Currency = %q, want %q", res.Instrument.Currency, tc.wantCurrency)
			}
			if len(res.Identifiers) == 0 {
				t.Error("expected at least one identifier")
			}
			if tc.wantClass == "OPTION" && len(res.Instrument.UnderlyingIdentifiers) == 0 {
				t.Error("expected UnderlyingIdentifiers for option")
			}
		})
	}
}
