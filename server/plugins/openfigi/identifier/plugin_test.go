package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
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
				FIGI:         "BBG000BLNNH6",
				Ticker:       "IBM",
				Name:         "INTL BUSINESS MACHINES CORP",
				ExchCode:     "US",
				SecurityType: "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector: "Equity",
			}}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "IBM"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "STOCK" || inst.Name != "INTL BUSINESS MACHINES CORP" || inst.Exchange != "" {
		t.Errorf("instrument = %+v", inst)
	}
	hasFIGI, hasOpenFIGITicker, hasMICTicker := false, false, false
	for _, id := range ids {
		if id.Type == "OPENFIGI_GLOBAL" && id.Value == "BBG000BLNNH6" {
			hasFIGI = true
		}
		if id.Type == "OPENFIGI_TICKER" && id.Value == "IBM" && id.Domain == "US" {
			hasOpenFIGITicker = true
		}
		if id.Type == "MIC_TICKER" && id.Value == "IBM" {
			hasMICTicker = true
		}
	}
	if !hasFIGI || !hasOpenFIGITicker || !hasMICTicker {
		t.Errorf("identifiers = %+v; want OPENFIGI_GLOBAL, OPENFIGI_TICKER, and MIC_TICKER", ids)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5S399"}}
	inst, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "INTL BUSINESS MACHINES CORP" {
		t.Errorf("instrument = %+v", inst)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "Apple Inc", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.Name != "APPLE INC" {
		t.Errorf("inst.Name = %q", inst.Name)
	}
	if len(ids) < 3 {
		t.Errorf("expected OPENFIGI_GLOBAL, OPENFIGI_TICKER, and MIC_TICKER, got %+v", ids)
	}
	hasMICTicker := false
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value == "AAPL" {
			hasMICTicker = true
		}
	}
	if !hasMICTicker {
		t.Errorf("expected MIC_TICKER:AAPL in identifiers, got %+v", ids)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "ABNB", Domain: "XNAS"}}
	inst, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "ABNB", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "AIRBNB INC-CLASS A" {
		t.Errorf("instrument = %+v", inst)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK.B"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK B BERKSHIRE HATHAWAY INC-CL B", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", inst)
	}
	// Returned OPENFIGI_TICKER identifier should use canonical dot separator.
	for _, id := range ids {
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK-B"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK-B", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", inst)
	}
	for _, id := range ids {
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	inst, ids, err := p.Identify(ctx, []byte("{}"), "IBKR", "IBKR:test:statement", "Apple Inc", identifier.Hints{}, nil)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if inst != nil || ids != nil {
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "UNKNOWN"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "UNKNOWN THING XYZ", identifier.Hints{}, hints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if inst != nil || ids != nil {
		t.Errorf("expected nil result")
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "SOMEUNKNOWN"}}
	_, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "SOME UNKNOWN", identifier.Hints{}, hints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v", err)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Value: "UNPARSEABLE"}}
	_, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "Some Exotic Option",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}, hints)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00200000"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "OPTION" {
		t.Errorf("inst.AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if len(inst.UnderlyingIdentifiers) != 1 || inst.UnderlyingIdentifiers[0].Value != "AAPL" {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{MIC_TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
	hasOCC := false
	for _, id := range ids {
		if id.Type == "OCC" && id.Value == "AAPL251219C00200000" {
			hasOCC = true
		}
		if id.Type == "OPENFIGI_TICKER" {
			t.Errorf("unexpected OPENFIGI_TICKER identifier for option: %+v", id)
		}
	}
	if !hasOCC {
		t.Errorf("expected OCC identifier, got %+v", ids)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	// Pass OCC with space-padding already present.
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL  251219C00200000"}}
	inst, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "OPTION" {
		t.Errorf("inst.AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if len(inst.UnderlyingIdentifiers) != 1 || inst.UnderlyingIdentifiers[0].Value != "AAPL" {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{MIC_TICKER AAPL}]", inst.UnderlyingIdentifiers)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "OCC", Value: "AAPL251219C00200000"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "AAPL Dec 2025 200 Call",
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "OPTION" {
		t.Errorf("inst.AssetClass = %q, want OPTION", inst.AssetClass)
	}
	if len(inst.UnderlyingIdentifiers) != 1 || inst.UnderlyingIdentifiers[0].Value != "AAPL" {
		t.Errorf("UnderlyingIdentifiers = %+v, want [{MIC_TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
	hasOCC := false
	for _, id := range ids {
		if id.Type == "OCC" && id.Value == "AAPL251219C00200000" {
			hasOCC = true
		}
		if id.Type == "OPENFIGI_TICKER" {
			t.Errorf("unexpected OPENFIGI_TICKER identifier for option: %+v", id)
		}
	}
	if !hasOCC {
		t.Errorf("expected OCC identifier AAPL251219C00200000, got %+v", ids)
	}
}

func TestResolveResults_HintMatchesOneResult(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_ETF", Ticker: "X", SecurityType: "ETP", SecurityType2: "ETP", MarketSector: "Equity"},
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, ids, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, true)
	if !ok || inst == nil {
		t.Fatal("expected result")
	}
	if inst.AssetClass != "ETF" {
		t.Errorf("AssetClass = %q, want ETF", inst.AssetClass)
	}
	hasFIGI := false
	for _, id := range ids {
		if id.Type == "OPENFIGI_GLOBAL" && id.Value == "BBG_ETF" {
			hasFIGI = true
		}
	}
	if !hasFIGI {
		t.Errorf("expected OPENFIGI_GLOBAL=BBG_ETF in ids: %+v", ids)
	}
}

func TestResolveResults_HintMatchesNone_FallsBackToFirst(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, ids, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "ETF"}, true)
	if !ok || inst == nil {
		t.Fatal("expected result (fallback to first)")
	}
	if inst.AssetClass != "STOCK" {
		t.Errorf("AssetClass = %q, want STOCK (first result)", inst.AssetClass)
	}
	hasFIGI := false
	for _, id := range ids {
		if id.Type == "OPENFIGI_GLOBAL" && id.Value == "BBG_STOCK" {
			hasFIGI = true
		}
	}
	if !hasFIGI {
		t.Errorf("expected OPENFIGI_GLOBAL=BBG_STOCK in ids: %+v", ids)
	}
}

func TestResolveResults_NoHint_FallsBackToFirst(t *testing.T) {
	results := []OpenFIGIResult{
		{FIGI: "BBG_BOND", Ticker: "X", SecurityType: "Bond", SecurityType2: "Corp", MarketSector: "Corp"},
		{FIGI: "BBG_STOCK", Ticker: "X", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity"},
	}
	p := NewPlugin(nil, nil, nil)
	inst, _, ok := p.resolveResults(results, identifier.Hints{}, true)
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
	inst, _, ok := p.resolveResults(results, identifier.Hints{SecurityTypeHint: "STOCK"}, true)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "EQQQ"}}
	_, ids, err := p.Identify(ctx, config, "Fidelity", "Fidelity:web:standard", "EQQQ", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	var found *identifier.Identifier
	for i, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value == "EQQQ" {
			found = &ids[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected MIC_TICKER:EQQQ in identifiers, got %+v", ids)
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
	p := NewPlugin(nil, nil, http.DefaultClient)
	hints := []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5S399"}}
	_, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	for _, id := range ids {
		if id.Type == "MIC_TICKER" {
			t.Errorf("unexpected MIC_TICKER in identifiers when hint was OPENFIGI_SHARE_CLASS: %+v", ids)
		}
	}
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
