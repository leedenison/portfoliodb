package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

func TestPlugin_Identify_Stock_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := client.APIResponse[client.TickerOverviewResult]{
			Status: "OK",
			Results: client.TickerOverviewResult{
				Ticker:          "AAPL",
				Name:            "Apple Inc.",
				Market:          "stocks",
				PrimaryExchange: "XNAS",
				CurrencyName:    "usd",
				CompositeFIGI:   "BBG000B9XRY4",
				ShareClassFIGI:  "BBG001S5N8V8",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
	idHints := []identifier.Identifier{{Type: "TICKER", Value: "AAPL"}}

	inst, ids, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.AssetClass != db.AssetClassStock {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
	if inst.Name != "Apple Inc." {
		t.Errorf("Name = %q, want Apple Inc.", inst.Name)
	}
	if len(ids) != 3 {
		t.Fatalf("len(ids) = %d, want 3", len(ids))
	}
}

func TestPlugin_Identify_Stock_SplitTickerNormalized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the API receives dot-separated ticker.
		if r.URL.Path != "/v3/reference/tickers/BRK.B" {
			t.Errorf("expected path /v3/reference/tickers/BRK.B, got %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := client.APIResponse[client.TickerOverviewResult]{
			Status: "OK",
			Results: client.TickerOverviewResult{
				Ticker:          "BRK.B",
				Name:            "Berkshire Hathaway Inc Class B",
				Market:          "stocks",
				PrimaryExchange: "XNYS",
				CurrencyName:    "usd",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
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
			p := NewPlugin(nil, nil, http.DefaultClient)
			cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
			hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
			idHints := []identifier.Identifier{{Type: "TICKER", Value: tt.input}}

			inst, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
			if err != nil {
				t.Fatalf("Identify(%q): %v", tt.input, err)
			}
			if inst == nil || inst.Name != "Berkshire Hathaway Inc Class B" {
				t.Errorf("Identify(%q): inst = %+v", tt.input, inst)
			}
		})
	}
}

func TestPlugin_Identify_Stock_IndexReturnsNotIdentified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := client.APIResponse[client.TickerOverviewResult]{
			Status: "OK",
			Results: client.TickerOverviewResult{
				Ticker: "SPX",
				Name:   "S&P 500",
				Market: "indices",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
	idHints := []identifier.Identifier{{Type: "TICKER", Value: "SPX"}}

	_, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("expected ErrNotIdentified, got %v", err)
	}
}

func TestPlugin_Identify_NoHints(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	_, _, err := p.Identify(context.Background(), nil, "", "", "", identifier.Hints{}, nil)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("expected ErrNotIdentified, got %v", err)
	}
}

func TestPlugin_Identify_NoTickerHint(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: "http://unused"})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
	idHints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}

	_, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("expected ErrNotIdentified, got %v", err)
	}
}

func TestPlugin_Identify_Option_OCC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/reference/options/contracts/O:AAPL251219C00230000":
			resp := client.APIResponse[client.OptionsContractResult]{
				Status: "OK",
				Results: client.OptionsContractResult{
					Ticker:            "O:AAPL251219C00230000",
					UnderlyingTicker:  "AAPL",
					ContractType:      "call",
					ExerciseStyle:     "american",
					ExpirationDate:    "2025-12-19",
					StrikePrice:       230.0,
					SharesPerContract: 100,
					PrimaryExchange:   "BATO",
				},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}
	idHints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00230000"}}

	inst, ids, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.AssetClass != db.AssetClassOption {
		t.Errorf("AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if len(inst.UnderlyingIdentifiers) != 1 || inst.UnderlyingIdentifiers[0].Value != "AAPL" {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
	if len(ids) != 2 {
		t.Fatalf("len(ids) = %d, want 2", len(ids))
	}
}

func TestPlugin_Identify_Option_OCC_SpacePadded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/reference/options/contracts/O:AAPL251219C00230000":
			resp := client.APIResponse[client.OptionsContractResult]{
				Status: "OK",
				Results: client.OptionsContractResult{
					Ticker:            "O:AAPL251219C00230000",
					UnderlyingTicker:  "AAPL",
					ContractType:      "call",
					ExerciseStyle:     "american",
					ExpirationDate:    "2025-12-19",
					StrikePrice:       230.0,
					SharesPerContract: 100,
					PrimaryExchange:   "BATO",
				},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}
	// Pass OCC with space-padding (21-char format).
	idHints := []identifier.Identifier{{Type: "OCC", Value: "AAPL  251219C00230000"}}

	inst, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.AssetClass != db.AssetClassOption {
		t.Errorf("AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if len(inst.UnderlyingIdentifiers) != 1 || inst.UnderlyingIdentifiers[0].Value != "AAPL" {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
}

func TestPlugin_Identify_Option_NoUnderlyingTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := client.APIResponse[client.OptionsContractResult]{
			Status: "OK",
			Results: client.OptionsContractResult{
				Ticker:          "O:AAPL251219C00230000",
				PrimaryExchange: "BATO",
				// UnderlyingTicker intentionally empty.
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}
	idHints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00230000"}}

	_, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("expected ErrNotIdentified when no underlying_ticker, got %v", err)
	}
}

func TestPlugin_Identify_Option_NoOCC(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: "http://unused"})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}
	idHints := []identifier.Identifier{{Type: "TICKER", Value: "AAPL"}}

	_, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("expected ErrNotIdentified for option without OCC hint, got %v", err)
	}
}

func TestPlugin_Identify_429_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := mustMarshal(t, configJSON{MassiveBaseURL: srv.URL})
	hints := identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}
	idHints := []identifier.Identifier{{Type: "TICKER", Value: "AAPL"}}

	_, _, err := p.Identify(context.Background(), cfg, "", "", "", hints, idHints)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	var rlErr *client.ErrRateLimit
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected ErrRateLimit, got %T: %v", err, err)
	}
}

func TestPlugin_DefaultConfig(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	cfg := p.DefaultConfig()
	var parsed configJSON
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid default config JSON: %v", err)
	}
	if parsed.MassiveAPIKey != "" {
		t.Errorf("default API key should be empty, got %q", parsed.MassiveAPIKey)
	}
}

func TestPlugin_AcceptableSecurityTypes(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	types := p.AcceptableSecurityTypes()
	if !types[identifier.SecurityTypeHintStock] {
		t.Error("expected STOCK to be acceptable")
	}
	if !types[identifier.SecurityTypeHintOption] {
		t.Error("expected OPTION to be acceptable")
	}
	if types[identifier.SecurityTypeHintFuture] {
		t.Error("FUTURE should not be acceptable")
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
