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
	cfg, err := json.Marshal(configJSON{
		EODHDAPIKey:  "test-key",
		EODHDBaseURL: baseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPlugin_Identify_Stock_Success(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`
	fundResp := `{"Code":"AAPL","Name":"Apple Inc","Exchange":"US","CurrencyCode":"USD","ISIN":"US0378331005","CUSIP":"037833100"}`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/search/AAPL":
			w.Write([]byte(searchResp))
		case r.URL.Path == "/api/fundamentals/AAPL.US":
			w.Write([]byte(fundResp))
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	inst, ids, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
	if inst.Name != "Apple Inc" {
		t.Errorf("Name = %q, want Apple Inc", inst.Name)
	}

	hasCUSIP := false
	for _, id := range ids {
		if id.Type == "CUSIP" {
			hasCUSIP = true
		}
	}
	if !hasCUSIP {
		t.Error("expected CUSIP identifier from fundamentals")
	}
	if len(ids) < 3 {
		t.Errorf("got %d identifiers, want at least 3 (TICKER+ISIN+CUSIP)", len(ids))
	}
}

func TestPlugin_Identify_ISIN_Fallback(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`
	fundResp := `{"Code":"AAPL","Name":"Apple Inc","Exchange":"US","CurrencyCode":"USD","ISIN":"US0378331005","CUSIP":"037833100"}`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/search/US0378331005":
			w.Write([]byte(searchResp))
		case r.URL.Path == "/api/fundamentals/AAPL.US":
			w.Write([]byte(fundResp))
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	inst, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		[]identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
}

func TestPlugin_Identify_NoHints(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := testConfig(t, "http://unused")

	_, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		nil,
	)

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
}

func TestPlugin_Identify_NoTickerOrISIN(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := testConfig(t, "http://unused")

	_, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		[]identifier.Identifier{{Type: "OCC", Value: "AAPL260316C00252500"}},
	)

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
}

func TestPlugin_Identify_429_PropagatesError(t *testing.T) {
	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	_, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
	)

	if err == nil {
		t.Fatal("expected error")
	}
	var rl *eodhdclient.ErrRateLimit
	if !errors.As(err, &rl) {
		t.Errorf("got err type %T, want *client.ErrRateLimit", err)
	}
}

func TestPlugin_Identify_EmptyResults(t *testing.T) {
	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	_, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
	)

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
}

func TestPlugin_Identify_NonStockFiltered(t *testing.T) {
	searchResp := `[{"Code":"SPY","Exchange":"US","Name":"SPDR S&P 500","Type":"ETF","Currency":"USD","ISIN":"US78462F1030","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(searchResp))
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	_, _, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "SPY"}},
	)

	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("got err=%v, want ErrNotIdentified", err)
	}
}

func TestPlugin_Identify_FundamentalsFailure_StillSucceeds(t *testing.T) {
	searchResp := `[{"Code":"AAPL","Exchange":"US","Name":"Apple Inc","Type":"Common Stock","Currency":"USD","ISIN":"US0378331005","isPrimary":true}]`

	srv, httpClient := testServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/search/AAPL":
			w.Write([]byte(searchResp))
		case r.URL.Path == "/api/fundamentals/AAPL.US":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()

	p := NewPlugin(nil, nil, httpClient)
	cfg := testConfig(t, srv.URL)

	inst, ids, err := p.Identify(context.Background(), cfg, "broker", "source", "desc",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument even when fundamentals fails")
	}
	// Should still have TICKER + ISIN, just no CUSIP.
	if len(ids) != 2 {
		t.Errorf("got %d identifiers, want 2 (no CUSIP when fundamentals fails)", len(ids))
	}
}

func TestPlugin_DefaultConfig(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	cfg := p.DefaultConfig()

	var parsed configJSON
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.EODHDAPIKey != "" {
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
