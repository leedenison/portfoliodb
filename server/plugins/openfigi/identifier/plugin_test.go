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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "IBM"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "STOCK" || inst.Name != "INTL BUSINESS MACHINES CORP" || inst.Exchange != "US" {
		t.Errorf("instrument = %+v", inst)
	}
	hasFIGI, hasTicker := false, false
	for _, id := range ids {
		if id.Type == "OPENFIGI_GLOBAL" && id.Value == "BBG000BLNNH6" {
			hasFIGI = true
		}
		if id.Type == "TICKER" && id.Value == "IBM" && id.Domain == "US" {
			hasTicker = true
		}
	}
	if !hasFIGI || !hasTicker {
		t.Errorf("identifiers = %+v", ids)
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "AAPL"}}
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
	if len(ids) < 2 {
		t.Errorf("expected OPENFIGI_GLOBAL and TICKER, got %+v", ids)
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "BRK.B"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK B BERKSHIRE HATHAWAY INC-CL B", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", inst)
	}
	// Returned TICKER identifier should use canonical dot separator.
	for _, id := range ids {
		if id.Type == "TICKER" {
			if id.Value != "BRK.B" {
				t.Errorf("returned TICKER value = %q, want canonical %q", id.Value, "BRK.B")
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "BRK-B"}}
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "BRK-B", identifier.Hints{}, hints)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "BERKSHIRE HATHAWAY INC-CL B" {
		t.Errorf("instrument = %+v", inst)
	}
	for _, id := range ids {
		if id.Type == "TICKER" {
			if id.Value != "BRK.B" {
				t.Errorf("returned TICKER value = %q, want canonical %q", id.Value, "BRK.B")
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "UNKNOWN"}}
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "SOMEUNKNOWN"}}
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
	hints := []identifier.Identifier{{Type: "TICKER", Value: "UNPARSEABLE"}}
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
		t.Errorf("UnderlyingIdentifiers = %+v, want [{TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
	if len(ids) == 0 {
		t.Error("expected identifiers")
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
		t.Errorf("UnderlyingIdentifiers = %+v, want [{TICKER AAPL}]", inst.UnderlyingIdentifiers)
	}
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
