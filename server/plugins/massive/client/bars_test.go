package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDailyBars_Success(t *testing.T) {
	bars := []AggBar{
		{O: 100.0, H: 105.0, L: 99.0, C: 103.0, V: 1000, VW: 102.0, T: 1700000000000, N: 50},
		{O: 103.0, H: 107.0, L: 102.0, C: 106.0, V: 1200, VW: 104.0, T: 1700086400000, N: 60},
	}
	resp := APIResponse[[]AggBar]{Status: "OK", Results: bars}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/aggs/ticker/AAPL/range/1/day/2024-01-01/2024-01-31" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("adjusted") != "false" {
			t.Error("expected adjusted=false")
		}
		if r.URL.Query().Get("sort") != "asc" {
			t.Error("expected sort=asc")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, NewRateLimiter(0), nil, srv.Client())
	got, err := c.DailyBars(context.Background(), "AAPL", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("DailyBars: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 bars, got %d", len(got))
	}
	if got[0].C != 103.0 || got[1].C != 106.0 {
		t.Errorf("unexpected close prices: %v, %v", got[0].C, got[1].C)
	}
}

func TestDailyBars_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, NewRateLimiter(0), nil, srv.Client())
	_, err := c.DailyBars(context.Background(), "INVALID", "2024-01-01", "2024-01-31")
	if err == nil {
		t.Fatal("expected error")
	}
	var nf *ErrNotFound
	if !isErrNotFound(err, &nf) {
		t.Errorf("expected ErrNotFound, got %T: %v", err, err)
	}
}

func TestDailyBars_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, NewRateLimiter(0), nil, srv.Client())
	_, err := c.DailyBars(context.Background(), "AAPL", "2024-01-01", "2024-01-31")
	if err == nil {
		t.Fatal("expected error")
	}
	var rl *ErrRateLimit
	if !isErrRateLimit(err, &rl) {
		t.Errorf("expected ErrRateLimit, got %T: %v", err, err)
	}
}

func TestDailyBars_OptionTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/aggs/ticker/O:AAPL250321C00150000/range/1/day/2024-01-01/2024-01-31" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := APIResponse[[]AggBar]{Status: "OK", Results: []AggBar{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, NewRateLimiter(0), nil, srv.Client())
	_, err := c.DailyBars(context.Background(), "O:AAPL250321C00150000", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("DailyBars: %v", err)
	}
}

func isErrNotFound(err error, target **ErrNotFound) bool {
	e, ok := err.(*ErrNotFound)
	if ok {
		*target = e
	}
	return ok
}

func isErrRateLimit(err error, target **ErrRateLimit) bool {
	e, ok := err.(*ErrRateLimit)
	if ok {
		*target = e
	}
	return ok
}
