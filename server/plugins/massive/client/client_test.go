package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTickerOverview_Success(t *testing.T) {
	want := TickerOverviewResult{
		Ticker:          "AAPL",
		Name:            "Apple Inc.",
		Market:          "stocks",
		Type:            "CS",
		Active:          true,
		PrimaryExchange: "XNAS",
		CurrencyName:    "usd",
		CompositeFIGI:   "BBG000B9XRY4",
		ShareClassFIGI:  "BBG001S5N8V8",
		ListDate:        "1980-12-12",
		TickerRoot:      "AAPL",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/reference/tickers/AAPL" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("apiKey") != "test-key" {
			t.Errorf("missing apiKey param")
		}
		resp := APIResponse[TickerOverviewResult]{Status: "OK", RequestID: "r1", Count: 1, Results: want}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, nil, nil, http.DefaultClient)
	got, err := c.TickerOverview(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Ticker != want.Ticker || got.Name != want.Name || got.Market != want.Market {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.CompositeFIGI != want.CompositeFIGI {
		t.Errorf("CompositeFIGI = %q, want %q", got.CompositeFIGI, want.CompositeFIGI)
	}
}

func TestTickerOverview_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New("key", srv.URL, nil, nil, http.DefaultClient)
	_, err := c.TickerOverview(context.Background(), "AAPL")
	if err == nil {
		t.Fatal("expected error on 429")
	}
	var rlErr *ErrRateLimit
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected ErrRateLimit, got %T: %v", err, err)
	}
}

func TestTickerOverview_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status":"NOT_FOUND"}`))
	}))
	defer srv.Close()

	c := New("key", srv.URL, nil, nil, http.DefaultClient)
	_, err := c.TickerOverview(context.Background(), "ZZZZZ")
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestOptionsContract_Success(t *testing.T) {
	want := OptionsContractResult{
		Ticker:            "O:AAPL251219C00230000",
		UnderlyingTicker:  "AAPL",
		ContractType:      "call",
		ExerciseStyle:     "american",
		ExpirationDate:    "2025-12-19",
		StrikePrice:       230.0,
		SharesPerContract: 100,
		PrimaryExchange:   "BATO",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/reference/options/contracts/O:AAPL251219C00230000" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := APIResponse[OptionsContractResult]{Status: "OK", RequestID: "r2", Results: want}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("key", srv.URL, nil, nil, http.DefaultClient)
	got, err := c.OptionsContract(context.Background(), "O:AAPL251219C00230000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.UnderlyingTicker != "AAPL" || got.ContractType != "call" || got.StrikePrice != 230.0 {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestOptionsContract_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New("key", srv.URL, nil, nil, http.DefaultClient)
	_, err := c.OptionsContract(context.Background(), "O:AAPL251219C00230000")
	if err == nil {
		t.Fatal("expected error on 429")
	}
	var rlErr *ErrRateLimit
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected ErrRateLimit, got %T: %v", err, err)
	}
}
