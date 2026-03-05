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
	p := NewPlugin(nil, nil)
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "IBM", "")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil {
		t.Fatal("expected instrument")
	}
	if inst.AssetClass != "EQUITY" || inst.Name != "INTL BUSINESS MACHINES CORP" || inst.Exchange != "US" {
		t.Errorf("instrument = %+v", inst)
	}
	hasFIGI, hasSYMBOL := false, false
	for _, id := range ids {
		if id.Type == "FIGI" && id.Value == "BBG000BLNNH6" {
			hasFIGI = true
		}
		if id.Type == "SYMBOL" && id.Value == "IBM" {
			hasSYMBOL = true
		}
	}
	if !hasFIGI || !hasSYMBOL {
		t.Errorf("identifiers = %+v", ids)
	}
}

func TestPlugin_Identify_OpenFIGISearch_OneResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Query != "Apple Inc" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SearchResponse{
			Data: []OpenFIGIResult{{
				FIGI:          "BBG000B9XRY4",
				Ticker:        "AAPL",
				Name:          "APPLE INC",
				ExchCode:      "US",
				SecurityType:  "Common Stock",
				SecurityType2: "Common Stock",
				MarketSector:  "Equity",
			}},
		})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "test-key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, nil)
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "Apple Inc", "")
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
		t.Errorf("expected FIGI and SYMBOL, got %+v", ids)
	}
}

func TestPlugin_Identify_OpenFIGIZero_OpenAINormalize_ThenSuccess(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/search" {
			searchCalls++
			var req SearchRequest
			json.NewDecoder(r.Body).Decode(&req)
			if searchCalls == 1 && req.Query != "APPLE INC COM" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			if searchCalls == 1 {
				json.NewEncoder(w).Encode(SearchResponse{Data: nil})
				return
			}
			http.Error(w, "unexpected second search", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/v3/mapping" {
			var jobs []MappingJob
			if err := json.NewDecoder(r.Body).Decode(&jobs); err != nil || len(jobs) != 1 || jobs[0].IDValue != "AAPL" {
				http.Error(w, "expected mapping for AAPL", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode([]MappingResponseItem{
				{Data: []OpenFIGIResult{{
					FIGI: "BBG000B9XRY4", Ticker: "AAPL", Name: "APPLE INC",
					ExchCode: "US", SecurityType: "Common Stock", SecurityType2: "Common Stock", MarketSector: "Equity",
				}}},
			})
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"content": "AAPL"}},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "key",
		"openfigi_base_url": server.URL,
		"openai_api_key":    "key",
		"openai_base_url":   server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, nil)
	inst, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "APPLE INC COM", "")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if inst == nil || inst.Name != "APPLE INC" {
		t.Errorf("expected APPLE INC instrument, got %+v", inst)
	}
	if searchCalls != 1 {
		t.Errorf("expected 1 search call (then OpenAI, then mapping), got %d", searchCalls)
	}
}

func TestPlugin_Identify_ErrNotIdentified_WhenNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/search" {
			json.NewEncoder(w).Encode(SearchResponse{Data: nil})
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
	p := NewPlugin(nil, nil)
	inst, ids, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "UNKNOWN THING XYZ", "")
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
	if inst != nil || ids != nil {
		t.Errorf("expected nil result")
	}
}

func TestPlugin_Identify_NoOpenAIKey_ReturnsErrNotIdentified_WhenOpenFIGIFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(SearchResponse{Data: nil})
	}))
	defer server.Close()

	config := mustJSON(map[string]string{
		"openfigi_api_key":  "key",
		"openfigi_base_url": server.URL,
	})
	ctx := context.Background()
	p := NewPlugin(nil, nil)
	_, _, err := p.Identify(ctx, config, "IBKR", "IBKR:test:statement", "SOME UNKNOWN", "")
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v", err)
	}
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
