//go:build integration

package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

func massiveSanitize(i *cassette.Interaction) error {
	u, err := url.Parse(i.Request.URL)
	if err != nil {
		return err
	}
	q := u.Query()
	if q.Has("apiKey") {
		q.Set("apiKey", "REDACTED")
		u.RawQuery = q.Encode()
		i.Request.URL = u.String()
	}
	return nil
}

func TestIntegration_Massive_Identify(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "MASSIVE_API_KEY")

	tests := []struct {
		name      string
		cassette  string
		hints     identifier.Hints
		idHints   []identifier.Identifier
		wantClass string // expected AssetClass, empty means ErrNotIdentified
		wantErr   error
	}{
		{
			name:     "stock_aapl",
			cassette: "testdata/cassettes/stock_aapl",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "TICKER", Value: "AAPL"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "stock_brk_b",
			cassette: "testdata/cassettes/stock_brk_b",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
			idHints: []identifier.Identifier{
				{Type: "TICKER", Value: "BRK.B"},
			},
			wantClass: "STOCK",
		},
		{
			name:     "option_aapl_call",
			cassette: "testdata/cassettes/option_aapl_call",
			hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption},
			idHints: []identifier.Identifier{
				{Type: "OCC", Value: "AAPL260316C00252500"},
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
			_, httpClient := vcr.New(t, tc.cassette, massiveSanitize)

			p := NewPlugin(nil, nil, httpClient)
			cfg, err := json.Marshal(configJSON{
				MassiveAPIKey: apiKey,
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
			if tc.wantClass == "OPTION" && inst.Underlying == nil {
				t.Error("expected underlying instrument for option")
			}
		})
	}
}
