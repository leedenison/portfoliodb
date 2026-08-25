package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	eodhdclient "github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
)

func testServer(handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	srv := httptest.NewServer(handler)
	return srv, srv.Client()
}

func testConfig(t *testing.T, baseURL string) []byte {
	t.Helper()
	cfg, err := json.Marshal(eodhdclient.Config{
		APIKey:  "test-key",
		BaseURL: baseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPlugin_Identify_Stock_Success(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instrument.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", res.Instrument.AssetClass)
	}
	if res.Instrument.Name != "Apple Inc" {
		t.Errorf("Name = %q, want Apple Inc", res.Instrument.Name)
	}
	if len(res.Identifiers) != 2 {
		t.Errorf("got %d identifiers, want 2 (MIC_TICKER+ISIN)", len(res.Identifiers))
	}
	if res.Telemetry.Outcome != identifier.OutcomeIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeIdentified)
	}
}

func TestPlugin_Identify_ISIN_Fallback(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}, Hints: identifier.Hints{}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", res.Instrument.AssetClass)
	}
}

func TestPlugin_Identify_SplitTickerNormalized(t *testing.T) {
	searchResp := `[{"Code":"BRK-B","Exchange":"US","Name":"Berkshire Hathaway","Type":"Common Stock","Currency":"USD","ISIN":"US0846707026","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		// Verify the API receives dash-separated ticker in the query path.
		if q := r.URL.Path; q != "/api/search/BRK-B" {
			t.Errorf("expected /api/search/BRK-B, got %s", q)
		}
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	tests := []struct {
		name  string
		input string
	}{
		{"slash separator", "BRK/B"},
		{"dash separator", "BRK-B"},
		{"space separator", "BRK B"},
		{"dot separator", "BRK.B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlugin(nil, httpClient, nil)
			cfg := testConfig(t, srv.URL)

			res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: tt.input}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}})
			if err != nil {
				t.Fatalf("Identify(%q): %v", tt.input, err)
			}
			if res.Instrument == nil {
				t.Fatalf("Identify(%q): nil instrument", tt.input)
			}
			// Returned MIC_TICKER identifier should use canonical dot.
			for _, id := range res.Identifiers {
				if id.Type == "MIC_TICKER" {
					if id.Value != "BRK.B" {
						t.Errorf("returned MIC_TICKER value = %q, want canonical %q", id.Value, "BRK.B")
					}
					break
				}
			}
		})
	}
}

func TestPlugin_Identify_NoHints(t *testing.T) {
	p := NewPlugin(nil, http.DefaultClient, nil)
	cfg := testConfig(t, "http://unused")

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: nil, Hints: identifier.Hints{}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_NoTickerOrISIN(t *testing.T) {
	p := NewPlugin(nil, http.DefaultClient, nil)
	cfg := testConfig(t, "http://unused")

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "OCC", Value: "AAPL260316C00252500"}}, Hints: identifier.Hints{}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_429_PropagatesError(t *testing.T) {
	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}})

	if err == nil {
		t.Fatal("expected error")
	}
	var rl *eodhdclient.ErrRateLimit
	if !errors.As(err, &rl) {
		t.Errorf("got err type %T, want *client.ErrRateLimit", err)
	}
	// A rate limit is its own outcome, not a generic failure: the resolver
	// cannot tell them apart from the error alone.
	if res.Telemetry.Outcome != identifier.OutcomeRateLimited {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeRateLimited)
	}
}

func TestPlugin_Identify_EmptyResults(t *testing.T) {
	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_NonStockFiltered(t *testing.T) {
	searchResp := `[{"Code":"SPY","Exchange":"US","Name":"SPDR S&P 500","Type":"ETF","Currency":"USD","ISIN":"US78462F1030","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "SPY"}}, Hints: identifier.Hints{}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_DefaultConfig(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	cfg := p.DefaultConfig()

	var parsed eodhdclient.Config
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.APIKey != "" {
		t.Error("default config should have empty API key")
	}
}

func TestPlugin_AcceptableSecurityTypes(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	types := p.AcceptableSecurityTypes()

	if !types[identifier.SecurityTypeHintStock] {
		t.Error("expected STOCK to be acceptable")
	}
	if len(types) != 1 {
		t.Errorf("got %d types, want 1 (STOCK only)", len(types))
	}
}

// The Search API matches on the name as readily as on the code, so a search for
// one symbol can answer with another company's listing. bestMatch takes it, and
// nothing downstream asks whether the answer was about the symbol that was
// asked for.
func TestPlugin_Identify_ATickerQueryIsVerifiedAgainstTheResult(t *testing.T) {
	searchResp := `[{"Code":"VODPF","Exchange":"US","Name":"Vodacom Group","Type":"Common Stock","Currency":"USD","ISIN":"ZAE000132577","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "VOD"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil {
		t.Errorf("Instrument = %+v, want nil: the search answered about another security", res.Instrument)
	}
}

// The two sides spell the class separator differently -- the query carries
// EODHD's dash and the provider wrote a dot -- and that is one symbol rather
// than two.
func TestPlugin_Identify_ATickerVerifiesAcrossSeparatorSpellings(t *testing.T) {
	searchResp := `[{"Code":"BRK.B","Exchange":"US","Name":"Berkshire Hathaway","Type":"Common Stock","Currency":"USD","ISIN":"US0846707026","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK-B"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected an instrument: BRK-B and BRK.B are one symbol")
	}
}

// The ISIN branch is what the ticker branch was modelled on, and it still holds.
func TestPlugin_Identify_AnISINQueryIsVerifiedAgainstTheResult(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, httpClient, nil)
	cfg := testConfig(t, srv.URL)

	res, err := p.Identify(context.Background(), cfg, "broker", "source", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "GB00BH4HKS39"}}, Hints: identifier.Hints{}})

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil {
		t.Errorf("Instrument = %+v, want nil", res.Instrument)
	}
}
