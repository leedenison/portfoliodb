package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
)

func TestPlugin_Identify_OpenFIGIMapping_OneResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDType != "TICKER" || jobs[0].IDValue != "IBM" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG000BLNNH6",
				Ticker:        "IBM",
				Name:          "INTL BUSINESS MACHINES CORP",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "IBM"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "STOCK" || res.Instrument.Name != "INTL BUSINESS MACHINES CORP" || res.Instrument.Listing.Venue.MIC != "" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
	hasOpenFIGITicker, hasMICTicker := false, false
	for _, id := range res.Identifiers {
		if id.Type == "OPENFIGI_TICKER" && id.Value == "IBM" && id.Domain == "US" {
			hasOpenFIGITicker = true
		}
		if id.Type == "MIC_TICKER" && id.Value == "IBM" {
			hasMICTicker = true
		}
	}
	if !hasOpenFIGITicker || !hasMICTicker {
		t.Errorf("identifiers = %+v; want OPENFIGI_TICKER and MIC_TICKER", res.Identifiers)
	}
	// FIGI should be in provider identifiers, not canonical.
	hasFIGI := false
	for _, pi := range res.Instrument.ProviderIdentifiers {
		if pi.Provider == "openfigi" && pi.Type == "FIGI" && pi.Value == "BBG000BLNNH6" {
			hasFIGI = true
		}
	}
	if !hasFIGI {
		t.Errorf("expected FIGI in ProviderIdentifiers, got %+v", res.Instrument.ProviderIdentifiers)
	}
}

func TestPlugin_Identify_OpenFIGIMapping_ID_BB_GLOBAL_SHARE_CLASS_LEVEL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if jobs[0].IDType != "ID_BB_GLOBAL_SHARE_CLASS_LEVEL" || jobs[0].IDValue != "BBG001S5S399" {
			t.Errorf("IDType = %q, IDValue = %q; want ID_BB_GLOBAL_SHARE_CLASS_LEVEL and BBG001S5S399", jobs[0].IDType, jobs[0].IDValue)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG001S5S399",
				Ticker:        "IBM",
				Name:          "INTL BUSINESS MACHINES CORP",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5S399"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil || res.Instrument.Name != "INTL BUSINESS MACHINES CORP" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
}

func TestPlugin_Identify_OpenFIGIMapping_FromTickerHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDType != "TICKER" || jobs[0].IDValue != "AAPL" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG000B9XRY4",
				Ticker:        "AAPL",
				Name:          "APPLE INC",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "Apple Inc", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.Name != "APPLE INC" {
		t.Errorf("res.Instrument.Name = %q", res.Instrument.Name)
	}
	if len(res.Identifiers) < 2 {
		t.Errorf("expected OPENFIGI_TICKER and MIC_TICKER, got %+v", res.Identifiers)
	}
	hasMICTicker := false
	for _, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" && id.Value == "AAPL" {
			hasMICTicker = true
		}
	}
	if !hasMICTicker {
		t.Errorf("expected MIC_TICKER:AAPL in identifiers, got %+v", res.Identifiers)
	}
}

func TestPlugin_Identify_OpenFIGIMapping_MICTickerDomainNotSentAsMICCode(t *testing.T) {
	// MIC_TICKER hints may carry a Domain (ISO 10383 MIC, e.g. "XNAS") set by
	// other plugins. The OpenFIGI plugin must NOT forward it as micCode because
	// OpenFIGI's MIC coverage is incomplete and the filter causes false negatives.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if jobs[0].MICCode != "" {
			t.Errorf("micCode = %q, want empty (should not be forwarded)", jobs[0].MICCode)
			// Return zero results to simulate the bug this test guards against.
			json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG001Y2XS07",
				Ticker:        "ABNB",
				Name:          "AIRBNB INC-CLASS A",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "ABNB", Domain: "XNAS"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "ABNB", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil || res.Instrument.Name != "AIRBNB INC-CLASS A" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
}

func TestPlugin_Identify_OpenFIGIMapping_TickerDotConvertedToSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDType != "TICKER" || jobs[0].IDValue != "BRK/B" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG000MM2P62",
				Ticker:        "BRK B",
				Name:          "BERKSHIRE HATHAWAY INC-CL B",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK.B"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK B BERKSHIRE HATHAWAY INC-CL B", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil || res.Instrument.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
	// Returned OPENFIGI_TICKER identifier should use canonical dot separator.
	for _, id := range res.Identifiers {
		if id.Type == "OPENFIGI_TICKER" {
			if id.Value != "BRK.B" {
				t.Errorf("returned OPENFIGI_TICKER value = %q, want canonical %q", id.Value, "BRK.B")
			}
			break
		}
	}
}

func TestPlugin_Identify_OpenFIGIMapping_TickerDashConvertedToSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDType != "TICKER" || jobs[0].IDValue != "BRK/B" {
			t.Errorf("expected TICKER BRK/B, got %s %s", jobs[0].IDType, jobs[0].IDValue)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG000MM2P62",
				Ticker:        "BRK/B",
				Name:          "BERKSHIRE HATHAWAY INC-CL B",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK-B"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK-B", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil || res.Instrument.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", res.Instrument)
	}
	for _, id := range res.Identifiers {
		if id.Type == "OPENFIGI_TICKER" {
			if id.Value != "BRK.B" {
				t.Errorf("returned OPENFIGI_TICKER value = %q, want canonical %q", id.Value, "BRK.B")
			}
			break
		}
	}
}

func TestPlugin_Identify_ErrNotIdentified_WhenNoHints(t *testing.T) {
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	res, err := p.Identify(ctx, []byte("{}"), "IBKR", "IBKR:test:statement", "Apple Inc", identifier.Identity{Stated: nil, Hints: identifier.Hints{}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil || res.Identifiers != nil {
		t.Errorf("expected nil result when no hints")
	}
}

func TestPlugin_Identify_ErrNotIdentified_WhenMappingReturnsNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/mapping" {
			json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "UNKNOWN"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "UNKNOWN THING XYZ", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if res.Instrument != nil || res.Identifiers != nil {
		t.Errorf("expected nil result")
	}
}

func TestPlugin_Identify_429_ReportsRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "APPLE INC", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	var rl *ErrRateLimit
	if !errors.As(err, &rl) {
		t.Fatalf("err = %T (%v), want *ErrRateLimit", err, err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeRateLimited {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeRateLimited)
	}
}

func TestPlugin_Identify_ErrNotIdentified_WhenMappingReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "SOMEUNKNOWN"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "SOME UNKNOWN", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_Option_ErrNotIdentified_WhenUnderlyingUnparseable(t *testing.T) {
	// OpenFIGI mapping returns an option result, but the derivative ticker can't
	// be parsed to extract the underlying symbol. The plugin should return ErrNotIdentified.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/mapping" {
			json.NewEncoder(w).Encode([]MappingResponseItem{
				{Data: []OpenFIGIResult{{
					FIGI:          "BBG00OPTION1",
					Ticker:        "UNPARSEABLE",
					Name:          "Some Exotic Option",
					ExchCode:      "US",
					SecurityType:  "Option",
					SecurityType2: "Equity Option",
					MarketSector:  "Equity",
				}}},
			})
		} else {
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "UNPARSEABLE"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "Some Exotic Option", identifier.Identity{Stated: hints, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if res.Telemetry.Outcome != identifier.OutcomeNotIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeNotIdentified)
	}
}

func TestPlugin_Identify_Option_WithUnderlying(t *testing.T) {
	// OpenFIGI mapping returns an option result. The plugin should return the option
	// with UnderlyingIdentifiers populated (underlying resolution is done by the
	// resolution layer, not the plugin).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if jobs[0].IDValue == "AAPL  251219C00200000" {
			json.NewEncoder(w).Encode([]MappingResponseItem{
				{Data: []OpenFIGIResult{{
					FIGI:          "BBG00OPTION1",
					Ticker:        "AAPL  251219C00200000",
					Name:          "AAPL Dec 2025 200 Call",
					ExchCode:      "US",
					SecurityType:  "Option",
					SecurityType2: "Equity Option",
					MarketSector:  "Equity",
				}}},
			})
		} else {
			json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
		}
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00200000"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call", identifier.Identity{Stated: hints, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "OPTION" {
		t.Errorf("res.Instrument.AssetClass = %q, want OPTION", res.Instrument.AssetClass)
	}
	if len(res.Instrument.UnderlyingIdentifiers) != 2 ||
		res.Instrument.UnderlyingIdentifiers[0] != (identifier.Identifier{Type: "OPENFIGI_TICKER", Domain: identifier.USComposite, Value: "AAPL"}) ||
		res.Instrument.UnderlyingIdentifiers[1] != (identifier.Identifier{Type: "MIC_TICKER", Value: "AAPL"}) {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{OPENFIGI_TICKER US AAPL} {MIC_TICKER AAPL}]", res.Instrument.UnderlyingIdentifiers)
	}
	hasOCC := false
	for _, id := range res.Identifiers {
		if id.Type == "OCC" && id.Value == "AAPL251219C00200000" {
			hasOCC = true
		}
		if id.Type == "OPENFIGI_TICKER" {
			t.Errorf("unexpected OPENFIGI_TICKER identifier for option: %+v", id)
		}
	}
	if !hasOCC {
		t.Errorf("expected OCC identifier, got %+v", res.Identifiers)
	}
}

func TestPlugin_Identify_Option_OCCSpacePadded(t *testing.T) {
	// When an OCC hint arrives with space-padding, the plugin should pad it
	// to the standard 21-char format and resolve successfully.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if jobs[0].IDValue == "AAPL  251219C00200000" {
			json.NewEncoder(w).Encode([]MappingResponseItem{
				{Data: []OpenFIGIResult{{
					FIGI:          "BBG00OPTION1",
					Ticker:        "AAPL  251219C00200000",
					Name:          "AAPL Dec 2025 200 Call",
					ExchCode:      "US",
					SecurityType:  "Option",
					SecurityType2: "Equity Option",
					MarketSector:  "Equity",
				}}},
			})
		} else {
			json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
		}
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	// Pass OCC with space-padding already present.
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL  251219C00200000"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call", identifier.Identity{Stated: hints, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "OPTION" {
		t.Errorf("res.Instrument.AssetClass = %q, want OPTION", res.Instrument.AssetClass)
	}
	if len(res.Instrument.UnderlyingIdentifiers) != 2 ||
		res.Instrument.UnderlyingIdentifiers[0] != (identifier.Identifier{Type: "OPENFIGI_TICKER", Domain: identifier.USComposite, Value: "AAPL"}) ||
		res.Instrument.UnderlyingIdentifiers[1] != (identifier.Identifier{Type: "MIC_TICKER", Value: "AAPL"}) {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{OPENFIGI_TICKER US AAPL} {MIC_TICKER AAPL}]", res.Instrument.UnderlyingIdentifiers)
	}
}

func TestPlugin_Identify_Option_ClassicTickerConvertedToOCC(t *testing.T) {
	// OpenFIGI often returns Classic-format tickers for options (e.g. "AAPL 12/19/25 C200").
	// The plugin should convert these to OCC format identifiers.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG00OPTION2",
				Ticker:        "AAPL 12/19/25 C200",
				Name:          "AAPL Dec 2025 200 Call",
				ExchCode:      "US",
				SecurityType:  "Option",
				SecurityType2: "Equity Option",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00200000"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call", identifier.Identity{Stated: hints, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}
	if res.Instrument.AssetClass != "OPTION" {
		t.Errorf("res.Instrument.AssetClass = %q, want OPTION", res.Instrument.AssetClass)
	}
	if len(res.Instrument.UnderlyingIdentifiers) != 2 ||
		res.Instrument.UnderlyingIdentifiers[0] != (identifier.Identifier{Type: "OPENFIGI_TICKER", Domain: identifier.USComposite, Value: "AAPL"}) ||
		res.Instrument.UnderlyingIdentifiers[1] != (identifier.Identifier{Type: "MIC_TICKER", Value: "AAPL"}) {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{OPENFIGI_TICKER US AAPL} {MIC_TICKER AAPL}]", res.Instrument.UnderlyingIdentifiers)
	}
	hasOCC := false
	for _, id := range res.Identifiers {
		if id.Type == "OCC" && id.Value == "AAPL251219C00200000" {
			hasOCC = true
		}
		if id.Type == "OPENFIGI_TICKER" {
			t.Errorf("unexpected OPENFIGI_TICKER identifier for option: %+v", id)
		}
	}
	if !hasOCC {
		t.Errorf("expected OCC identifier AAPL251219C00200000, got %+v", res.Identifiers)
	}
}

func TestResolveResults_HintMatchesOneResult(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_ETF", Ticker: "X", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, ids, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, nil, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.AssetClass != "ETF" {
		t.Errorf("AssetClass = %q, want ETF", inst.AssetClass)
	}
	hasFIGI := false
	for _, pi := range inst.ProviderIdentifiers {
		if pi.Provider == "openfigi" && pi.Type == "FIGI" && pi.Value == "BBG_ETF" {
			hasFIGI = true
		}
	}
	if !hasFIGI {
		t.Errorf("expected FIGI=BBG_ETF in ProviderIdentifiers: %+v", inst.ProviderIdentifiers)
	}
	_ = ids // canonical ids no longer include FIGI
}

func TestResolveResults_HintMatchesNone_FallsBackToFirst(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, ids, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, nil, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result (fallback to first)")
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK (first result)", inst.AssetClass)
	}
	hasFIGI := false
	for _, pi := range inst.ProviderIdentifiers {
		if pi.Provider == "openfigi" && pi.Type == "FIGI" && pi.Value == "BBG_STOCK" {
			hasFIGI = true
		}
	}
	if !hasFIGI {
		t.Errorf("expected FIGI=BBG_STOCK in ProviderIdentifiers: %+v", inst.ProviderIdentifiers)
	}
	_ = ids
}

func TestResolveResults_NoHint_FallsBackToFirst(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, _, ok := p.resolveResults(results, identifier.Hints{}, nil, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.AssetClass != "FIXED_INCOME" {
		t.Errorf("AssetClass = %q, want FIXED_INCOME (first result)", inst.AssetClass)
	}
}

func TestResolveResults_AssetClassFromSelectedResult(t *testing.T) {
	// Hint is STOCK but the matching result has securityType="ADR" which
	// classifies as STOCK. Verify the stored asset class comes from classify(),
	// not from the hint string directly.
	results := []OpenFIGIResult{
		{FIGI: "BBG_ETF", Ticker: "X", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
		{FIGI: "BBG_ADR", Ticker: "X", SecurityType: "ADR", SecurityType2: "Depositary Receipt", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "STOCK"}, nil, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	// ADR classifies as STOCK via the rule table -- the hint matched, and the
	// stored asset class is from classify(), which is also STOCK.
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
}

func TestPlugin_Identify_MICTickerWithDomainPreserved(t *testing.T) {
	// When a MIC_TICKER hint has a Domain (e.g. "XLON"), the returned identifier
	// should preserve that Domain so it can be stored with the correct exchange.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG00X2RM0W5",
				Ticker:        "EQQQ",
				Name:          "INVESCO NASDAQ-100 DIST",
				ExchCode:      "GR",
				SecurityType:  "ETP",
				SecurityType2: "ETP",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "EQQQ"}}
	res, err := p.Identify(ctx, config, "FIDELITY", "Fidelity:web:standard", "EQQQ", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	var found *identifier.Identifier
	for i, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" && id.Value == "EQQQ" {
			found = &res.Identifiers[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected MIC_TICKER:EQQQ in identifiers, got %+v", res.Identifiers)
	}
	if found.Domain != "XLON" {
		t.Errorf("MIC_TICKER domain = %q, want %q", found.Domain, "XLON")
	}
}

func TestPlugin_Identify_NonTickerHintNotAppended(t *testing.T) {
	// When the matched hint is not a MIC_TICKER (e.g. OPENFIGI_SHARE_CLASS),
	// the plugin should NOT append it to the returned identifiers.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG000BLNNH6",
				Ticker:        "IBM",
				Name:          "INTL BUSINESS MACHINES CORP",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)
	hints := []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5S399"}}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	for _, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" {
			t.Errorf("unexpected MIC_TICKER in identifiers when hint was OPENFIGI_SHARE_CLASS: %+v", res.Identifiers)
		}
	}
}

func TestPlugin_Identify_ExpiredOptionKeepsAtExpiryOCC(t *testing.T) {
	// A contract that expired before its underlying split was never restated,
	// so the OCC hint carries the strike it traded at ($510) and the identifier
	// the plugin returns is that same contract. Nothing rebases it to the
	// post-split strike the contract never had.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v3/mapping" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Expect the at-expiry value (padded to 21 chars).
		if jobs[0].IDValue == "NVDA  240315P00510000" {
			json.NewEncoder(w).Encode([]MappingResponseItem{
				{Data: []OpenFIGIResult{{
					FIGI:          "BBG00OPTION2",
					Ticker:        "NVDA  240315P00510000",
					Name:          "NVDA Mar 2024 510 Put",
					ExchCode:      "US",
					SecurityType:  "Option",
					SecurityType2: "Equity Option",
					MarketSector:  "Equity",
				}}},
			})
		} else {
			json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
		}
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, nil)

	hints := []identifier.Identifier{
		{Type: "OCC", Value: "NVDA240315P00510000"},
	}
	res, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "NVDA Mar 2024 510 Put", identifier.Identity{Stated: hints, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected instrument")
	}

	for _, id := range res.Identifiers {
		if id.Type == "OCC" {
			if id.Value != "NVDA240315P00510000" {
				t.Errorf("OCC = %q, want NVDA240315P00510000 (as it traded)", id.Value)
			}
			return
		}
	}
	t.Errorf("expected OCC identifier in %+v", res.Identifiers)
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestResolveResults_ExchangeHintOutranksSecurityType(t *testing.T) {
	// A bare ticker maps to every listing of that symbol. Two of them classify
	// as STOCK, so the security type alone cannot separate them; the exchange
	// the caller named can. Modelled on a UK ticker whose symbol is also used by
	// an unrelated Stockholm listing, where taking the first STOCK gave the
	// wrong company.
	results := []OpenFIGIResult{
		{FIGI: "BBG_SE", Ticker: "WISE", ExchCode: "SS", Name: "OTHER GROUP AB", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_GB", Ticker: "WISE", ExchCode: "LN", Name: "WISE PLC", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "WISE"}}
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "STOCK"}, hints, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.Name != "WISE PLC" {
		t.Errorf("Name = %q, want %q", inst.Name, "WISE PLC")
	}
	if inst.Listing.Venue.MIC != "XLON" {
		t.Errorf("Exchange = %q, want XLON", inst.Listing.Venue.MIC)
	}
}

func TestResolveResults_ExchangeHintPreferredOverTypeMatch(t *testing.T) {
	// The exchange is worth more than the security type: the hinted venue wins
	// even when a result on another venue is the one matching the type hint.
	results := []OpenFIGIResult{
		{FIGI: "BBG_US_ETF", Ticker: "X", ExchCode: "UW", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
		{FIGI: "BBG_GB_STOCK", Ticker: "X", ExchCode: "LN", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "X"}}
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, hints, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.Listing.Venue.MIC != "XLON" {
		t.Errorf("Exchange = %q, want XLON", inst.Listing.Venue.MIC)
	}
	// The asset class still comes from the selected result, not the hint.
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK", inst.AssetClass)
	}
}

func TestResolveResults_ExchangeHintUnmatched_FallsBackToType(t *testing.T) {
	// No result is on the hinted venue, so the security type decides as before.
	results := []OpenFIGIResult{
		{FIGI: "BBG_BOND", Ticker: "X", ExchCode: "UW", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
		{FIGI: "BBG_ETF", Ticker: "X", ExchCode: "UW", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "X"}}
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, hints, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.AssetClass != "ETF" {
		t.Errorf("AssetClass = %q, want ETF", inst.AssetClass)
	}
}

// figiPtr returns a pointer to s, for building a result's compositeFIGI.
func figiPtr(s string) *string { return &s }

// OpenFIGI answers a mapping call with rows from both of its code namespaces,
// and a US listing usually leads with the composite one. Read as a venue code
// it matched nothing, so every American result scored zero on exchange and the
// ranking fell back to security type -- no constraint at all for a ticker
// listed in a dozen countries, which is how a query settled on Vienna.
func TestResolveResults_CompositeCoveringHintedVenueOutranksAForeignListing(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_AT", Ticker: "BRK/B", ExchCode: "AV", Name: "OTHER AG", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_US", Ticker: "BRK/B", ExchCode: "US", Name: "BERKSHIRE HATHAWAY INC-CL B", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "BRK/B"}}
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "STOCK"}, hints, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("Name = %q, want the US listing", inst.Name)
	}
	// The composite says the listing is somewhere in that group, not that it is
	// on XNYS, so nothing is asserted about the venue.
	if inst.Listing.Venue.MIC != "" {
		t.Errorf("Exchange = %q, want empty: a composite names no single venue", inst.Listing.Venue.MIC)
	}
}

// A composite is the weaker claim of the two and must not outrank a result that
// names the venue outright, even when the composite also matches the type hint.
func TestResolveResults_NamedVenueOutranksCompositeThatCoversIt(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_COMPOSITE", Ticker: "X", ExchCode: "US", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_VENUE", Ticker: "X", ExchCode: "UN", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}}
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "STOCK"}, hints, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.Listing.Venue.MIC != "XNYS" {
		t.Errorf("Exchange = %q, want XNYS: naming the venue beats spanning it", inst.Listing.Venue.MIC)
	}
}

// With nothing to discriminate on, the composite is taken over whichever venue
// the provider happened to list first: it is the consolidated line for the
// market, and choosing it leaves the exchange unset rather than asserting a
// venue nobody named.
func TestResolveResults_UnrankedPrefersTheCompositeOverAnArbitraryVenue(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_VENUE", CompositeFIGI: figiPtr("BBG_COMPOSITE"), Ticker: "X", ExchCode: "UA", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_COMPOSITE", CompositeFIGI: figiPtr("BBG_COMPOSITE"), Ticker: "X", ExchCode: "US", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, exchangemap.New())
	inst, ids, ok := p.resolveResults(results, identifier.Hints{}, nil, nil, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.Listing.Venue.MIC != "" {
		t.Errorf("Exchange = %q, want empty", inst.Listing.Venue.MIC)
	}
	var domain string
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" {
			domain = id.Domain
		}
	}
	if domain != "US" {
		t.Errorf("OPENFIGI_TICKER domain = %q, want US: the composite row was chosen", domain)
	}
}

func TestPlugin_Identify_MICTickerDomainDroppedOnExchangeMismatch(t *testing.T) {
	// The mapping proves the ticker, not the venue. When the only result is on a
	// different exchange from the one the hint named, the hint is not stored
	// against it -- doing so would claim the London listing's ticker for a
	// Stockholm company, and every later lookup of that ticker would resolve to
	// the wrong instrument.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG_SE",
				Ticker:        "WISE",
				Name:          "OTHER GROUP AB",
				ExchCode:      "SS",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "WISE"}}
	res, err := p.Identify(ctx, config, "FIDELITY", "Fidelity:web:fidelity-csv", "WISE PLC (WISE)", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument.Listing.Venue.MIC != "XSTO" {
		t.Fatalf("Exchange = %q, want XSTO", res.Instrument.Listing.Venue.MIC)
	}
	for _, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" {
			t.Errorf("MIC_TICKER %+v asserted against an instrument on %s", id, res.Instrument.Listing.Venue.MIC)
		}
	}
}

func TestPlugin_Identify_MICTickerDomainKeptOnExchangeMatch(t *testing.T) {
	// The mirror of the mismatch case: the result is on the hinted venue, so the
	// hint is asserted with its domain intact.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI:          "BBG_GB",
				Ticker:        "WISE",
				Name:          "WISE PLC",
				ExchCode:      "LN",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, http.DefaultClient, exchangemap.New())
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "WISE"}}
	res, err := p.Identify(ctx, config, "FIDELITY", "Fidelity:web:fidelity-csv", "WISE PLC (WISE)", identifier.Identity{Stated: hints, Hints: identifier.Hints{}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	var found *identifier.Identifier
	for i, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" && id.Value == "WISE" {
			found = &res.Identifiers[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected MIC_TICKER:WISE in identifiers, got %+v", res.Identifiers)
	}
	if found.Domain != "XLON" {
		t.Errorf("MIC_TICKER domain = %q, want XLON", found.Domain)
	}
}

// A proposal ranks and does not resolve. The venue nobody stated picks between
// the listings the stated ticker already produced, and it must not come back as
// an identifier: the resolver stores what a plugin returns, so a proposal that
// came back would be indistinguishable from a confirmed name. See adr/0057.
func TestPlugin_Identify_ProposedVenueRanksButIsNotReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"data":[
			{"figi":"BBG_SE","name":"OTHER AB","ticker":"X","exchCode":"SS","securityType":"Common Stock","securityType2":"Common Stock","marketSector":"Equity"},
			{"figi":"BBG_GB","name":"REAL PLC","ticker":"X","exchCode":"LN","securityType":"Common Stock","securityType2":"Common Stock","marketSector":"Equity"}
		]}]`))
	}))
	defer srv.Close()

	p := NewPlugin(nil, srv.Client(), exchangemap.New())
	cfg := mustJSON(configJSON{OpenFIGIBaseURL: srv.URL})
	res, err := p.Identify(context.Background(), cfg, "b", "s", "desc", identifier.Identity{
		// The source named the ticker and no venue.
		Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}},
		// A plugin proposed the venue.
		Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "X"}},
		Hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
	})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if res.Instrument == nil {
		t.Fatal("expected an instrument")
	}
	// The proposal did its job: the London listing was chosen over the Stockholm
	// one, which nothing else in the call could have separated.
	if res.Instrument.Name != "REAL PLC" {
		t.Errorf("Name = %q, want the listing on the proposed venue", res.Instrument.Name)
	}
	// And it did not come back.
	for _, id := range res.Identifiers {
		if id.Type == "MIC_TICKER" && id.Domain == "XLON" {
			t.Errorf("proposed MIC_TICKER %+v was returned as an identifier", id)
		}
	}
}

// The mapping call filtered on the stated ISIN, so answering at all asserts the
// ISIN denotes the security in the answer. The plugin deliberately does not
// echo a matched ISIN back, because OpenFIGI may return a corrected value for
// that type -- which is exactly why the association has to travel as a filter
// rather than as a returned identifier. See adr/0060.
func TestPlugin_Identify_MatchedISINIsReportedAsFiltered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDType != "ID_ISIN" || jobs[0].IDValue != "US4592001014" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI: "BBG000BLNNH6", Ticker: "IBM", Name: "INTL BUSINESS MACHINES CORP",
				ExchCode: "US", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{"openfigi_api_key": "test-key", "openfigi_base_url": server.URL})
	p := NewPlugin(nil, http.DefaultClient, nil)
	res, err := p.Identify(context.Background(), config, "IBKR", "IBKR:test:statement", "IBM",
		identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "US4592001014"}}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(res.Filtered) != 1 || res.Filtered[0].Type != "ISIN" || res.Filtered[0].Value != "US4592001014" {
		t.Errorf("Filtered = %+v; want the stated ISIN", res.Filtered)
	}
	for _, id := range res.Identifiers {
		if id.Type == "ISIN" {
			t.Error("the matched ISIN was echoed back as a returned identifier")
		}
	}
}

// A MIC_TICKER hint is filtered on its value alone, because the domain is
// deliberately not sent as micCode. The claim must not say the venue was
// constrained when the request never constrained it.
func TestPlugin_Identify_FilteredMICTickerDropsTheVenue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var jobs []MappingJob
		if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].MICCode != "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{
			{Data: []OpenFIGIResult{{
				FIGI: "BBG000BLNNH6", Ticker: "IBM", Name: "INTL BUSINESS MACHINES CORP",
				ExchCode: "US", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{"openfigi_api_key": "test-key", "openfigi_base_url": server.URL})
	p := NewPlugin(nil, http.DefaultClient, nil)
	res, err := p.Identify(context.Background(), config, "IBKR", "IBKR:test:statement", "IBM",
		identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "IBM"}}})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(res.Filtered) != 1 || res.Filtered[0].Type != "MIC_TICKER" || res.Filtered[0].Value != "IBM" {
		t.Fatalf("Filtered = %+v", res.Filtered)
	}
	if res.Filtered[0].Domain != "" {
		t.Errorf("Filtered domain = %q; the MIC was never sent, so nothing constrained the venue", res.Filtered[0].Domain)
	}
}

// A call that got no answer filtered on nothing: the filter matching nothing is
// the provider declining to assert anything at all.
func TestPlugin_Identify_NoAnswerClaimsNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]MappingResponseItem{{Data: nil}})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{"openfigi_api_key": "test-key", "openfigi_base_url": server.URL})
	p := NewPlugin(nil, http.DefaultClient, nil)
	res, err := p.Identify(context.Background(), config, "IBKR", "IBKR:test:statement", "IBM",
		identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "US4592001014"}}})
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Fatalf("err = %v, want ErrNotIdentified", err)
	}
	if len(res.Filtered) != 0 {
		t.Errorf("Filtered = %+v; want none", res.Filtered)
	}
}
