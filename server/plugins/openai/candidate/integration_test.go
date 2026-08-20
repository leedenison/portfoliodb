//go:build integration

package candidate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/testutil/vcr"
)

// want is what one item's proposals must say. Empty fields are ones the model
// must have declined: a null is a correct answer here and the suite has to be
// able to require one.
type want struct {
	// Type of the single identifier expected, empty when nothing at all should
	// come back for the item.
	idType string
	value  string
	// domain is the venue, checked only when set. An exchange is the field this
	// prompt exists to add, so where it is knowable the test requires it.
	domain string
}

// This suite asserts values rather than identifier types. Asserting the type
// alone let a confidently wrong ticker pass, which is what 0105 complained
// about: every case here would have passed the old test whatever symbol the
// model returned.
func TestIntegration_OpenAI_ProposeBatch(t *testing.T) {
	apiKey := vcr.EnvOrSkip(t, "OPENAI_API_KEY", "openai/candidate")

	tests := []struct {
		name     string
		cassette string
		items    []candpkg.BatchItem
		want     map[string]want
	}{
		{
			// A description-only US listing. The exchange is the field the old
			// prompt could not return at all.
			name:     "nasdaq_stock",
			cassette: "testdata/cassettes/nasdaq_stock",
			items: []candpkg.BatchItem{
				{ID: "a1", InstrumentDescription: "AAPL APPLE INC", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock, Currency: "USD"}},
			},
			want: map[string]want{"a1": {idType: "MIC_TICKER", value: "AAPL", domain: "XNAS"}},
		},
		{
			name:     "nyse_stock",
			cassette: "testdata/cassettes/nyse_stock",
			items: []candpkg.BatchItem{
				{ID: "b1", InstrumentDescription: "BERKSHIRE HATHAWAY INC-CL B", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock, Currency: "USD"}},
			},
			want: map[string]want{"b1": {idType: "MIC_TICKER", value: "BRK.B", domain: "XNYS"}},
		},
		{
			// The QFX shape: a stated ISIN, a stated currency, no venue. The
			// point of 0131 is that this reaches the plugin at all, and the
			// point of this PR is that a venue comes back with it. The currency
			// is stated, so nothing may be proposed for it.
			name:     "lse_stock_from_isin",
			cassette: "testdata/cassettes/lse_stock_from_isin",
			items: []candpkg.BatchItem{
				{
					ID:                    "c1",
					InstrumentDescription: "SHELL PLC ORD EUR0.07",
					Hints:                 identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock, Currency: "GBX"},
					Stated:                []identifier.Identifier{{Type: "ISIN", Value: "GB00BP6MXD84"}},
				},
			},
			want: map[string]want{"c1": {idType: "MIC_TICKER", value: "SHEL", domain: "XLON"}},
		},
		{
			name:     "option",
			cassette: "testdata/cassettes/option",
			items: []candpkg.BatchItem{
				{ID: "d1", InstrumentDescription: "AAPL 19DEC25 230 C", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption, Currency: "USD"}},
			},
			want: map[string]want{"d1": {idType: "OCC", value: "AAPL251219C00230000"}},
		},
		{
			// Nothing real is named, so nothing may come back. A model that
			// answers here is inventing, and the null it is asked for instead is
			// what keeps a made-up ticker out of the resolution.
			name:     "unidentifiable",
			cassette: "testdata/cassettes/unidentifiable",
			items: []candpkg.BatchItem{
				{ID: "e1", InstrumentDescription: "MISC ADJUSTMENT REF 88213", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
			},
			want: map[string]want{"e1": {}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, httpClient := vcr.New(t, tc.cassette, vcr.SanitizeAll, "openai/candidate")

			p := NewPlugin(nil, httpClient)
			cfg, err := json.Marshal(configJSON{OpenAIAPIKey: apiKey, OpenAIModel: "gpt-4o-mini"})
			if err != nil {
				t.Fatalf("marshal config: %v", err)
			}

			res, err := p.ProposeBatch(context.Background(), cfg, "test-broker", "test-source", tc.items)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for id, w := range tc.want {
				ps := res.Proposed[id]
				if w.idType == "" {
					for _, pr := range ps {
						if pr.Identifier.Type != "CURRENCY" {
							t.Errorf("id %q: proposed %+v for a description naming no security", id, pr.Identifier)
						}
					}
					continue
				}
				var found *candpkg.Proposal
				for i := range ps {
					if ps[i].Identifier.Type == w.idType {
						found = &ps[i]
						break
					}
				}
				if found == nil {
					t.Fatalf("id %q: no %s proposal, got %+v", id, w.idType, ps)
				}
				if found.Identifier.Value != w.value {
					t.Errorf("id %q: value = %q, want %q", id, found.Identifier.Value, w.value)
				}
				if w.domain != "" && found.Identifier.Domain != w.domain {
					t.Errorf("id %q: domain = %q, want %q", id, found.Identifier.Domain, w.domain)
				}
				// Confidence is recorded and never gated on, but a field that
				// came back with a value and no confidence at all means the
				// model ignored the schema's confidence.
				if found.Confidence <= 0 || found.Confidence > 1 {
					t.Errorf("id %q: confidence = %v, want a value in (0, 1]", id, found.Confidence)
				}
			}
		})
	}
}
