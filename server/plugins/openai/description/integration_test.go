//go:build integration

package description

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

func TestIntegration_OpenAI_ExtractBatch(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "OPENAI_API_KEY")

	tests := []struct {
		name     string
		cassette string
		items    []descpkg.BatchItem
		wantType map[string]string // id -> expected identifier Type (TICKER or OCC)
	}{
		{
			name:     "stock_descriptions",
			cassette: "testdata/cassettes/stock_descriptions",
			items: []descpkg.BatchItem{
				{ID: "a1", InstrumentDescription: "AAPL APPLE INC", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
				{ID: "a2", InstrumentDescription: "MSFT MICROSOFT CORP", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
				{ID: "a3", InstrumentDescription: "GOOGL ALPHABET INC CL A", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
			},
			wantType: map[string]string{
				"a1": "TICKER",
				"a2": "TICKER",
				"a3": "TICKER",
			},
		},
		{
			name:     "option_description",
			cassette: "testdata/cassettes/option_description",
			items: []descpkg.BatchItem{
				{ID: "b1", InstrumentDescription: "AAPL 19DEC25 230 C", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}},
			},
			wantType: map[string]string{
				"b1": "OCC",
			},
		},
		{
			name:     "mixed_batch",
			cassette: "testdata/cassettes/mixed_batch",
			items: []descpkg.BatchItem{
				{ID: "c1", InstrumentDescription: "TSLA TESLA INC", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
				{ID: "c2", InstrumentDescription: "SPY 20DEC25 600 P", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}},
			},
			wantType: map[string]string{
				"c1": "TICKER",
				"c2": "OCC",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll)

			p := NewPlugin(nil, nil, httpClient)
			cfg, err := json.Marshal(configJSON{
				OpenAIAPIKey: apiKey,
				OpenAIModel:  "gpt-4o-mini",
			})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			out, err := p.ExtractBatch(
				context.Background(),
				cfg,
				"test-broker",
				"test-source",
				tc.items,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for id, wantType := range tc.wantType {
				ids, ok := out[id]
				if !ok {
					t.Errorf("missing result for id %q", id)
					continue
				}
				if len(ids) == 0 {
					t.Errorf("empty identifiers for id %q", id)
					continue
				}
				if ids[0].Type != wantType {
					t.Errorf("id %q: Type = %q, want %q", id, ids[0].Type, wantType)
				}
			}
		})
	}
}
