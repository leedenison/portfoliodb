package price

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

func barsServer(t *testing.T, bars []client.AggBar) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := client.APIResponse[[]client.AggBar]{Status: "OK", Results: bars}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func configWithURL(url string) []byte {
	cfg := configJSON{MassiveBaseURL: url}
	b, _ := json.Marshal(cfg)
	return b
}

func TestFetchPrices_Stock(t *testing.T) {
	bars := []client.AggBar{
		{O: 100, H: 105, L: 99, C: 103, V: 1000, T: 1704067200000}, // 2024-01-01
		{O: 103, H: 107, L: 102, C: 106, V: 1200, T: 1704153600000}, // 2024-01-02
	}
	srv := barsServer(t, bars)
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if len(result.Bars) != 2 {
		t.Fatalf("expected 2 bars, got %d", len(result.Bars))
	}
	if result.Bars[0].Close != 103 {
		t.Errorf("bar[0].Close = %v, want 103", result.Bars[0].Close)
	}
	if result.Bars[0].Open == nil || *result.Bars[0].Open != 100 {
		t.Error("bar[0].Open should be 100")
	}
}

func TestFetchPrices_Option(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		resp := client.APIResponse[[]client.AggBar]{
			Status:  "OK",
			Results: []client.AggBar{{O: 5, H: 6, L: 4, C: 5.5, V: 100, T: 1704067200000}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "OCC", Value: "AAPL250321C00150000"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassOption, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if len(result.Bars) != 1 {
		t.Fatalf("expected 1 bar, got %d", len(result.Bars))
	}
	// Verify the O: prefix was added.
	if requestedPath == "" {
		t.Fatal("no request made")
	}
	// URL path should contain the O: prefixed ticker.
	expected := "/v2/aggs/ticker/O:AAPL250321C00150000/range/1/day/2024-01-01/2024-01-01"
	if requestedPath != expected {
		t.Errorf("path = %q, want %q", requestedPath, expected)
	}
}

func TestFetchPrices_NoMatchingIdentifier(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient)
	ids := []pricefetcher.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), nil, ids, db.AssetClassStock, from, to)
	if err != pricefetcher.ErrNoData {
		t.Errorf("expected ErrNoData, got %v", err)
	}
}

func TestFetchPrices_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "INVALID"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent on 404, got %v", err)
	}
}

func TestFetchPrices_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("plan limit exceeded"))
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent on 403, got %v", err)
	}
}

func TestFetchPrices_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"status":"ERROR","error":"Unknown API Key"}`))
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent on 401, got %v", err)
	}
}

func TestFetchPrices_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if err == pricefetcher.ErrNoData {
		t.Error("429 should not be mapped to ErrNoData")
	}
}

func TestFetchPrices_FX(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		resp := client.APIResponse[[]client.AggBar]{
			Status:  "OK",
			Results: []client.AggBar{{O: 1.07, H: 1.09, L: 1.06, C: 1.08, V: 0, T: 1704067200000}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "EURUSD"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassFX, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if len(result.Bars) != 1 {
		t.Fatalf("expected 1 bar, got %d", len(result.Bars))
	}
	if result.Bars[0].Close != 1.08 {
		t.Errorf("bar[0].Close = %v, want 1.08", result.Bars[0].Close)
	}
	expected := "/v2/aggs/ticker/C:EURUSD/range/1/day/2024-01-01/2024-01-01"
	if requestedPath != expected {
		t.Errorf("path = %q, want %q", requestedPath, expected)
	}
}

func TestPluginInterface(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	if p.DisplayName() != "Massive" {
		t.Errorf("DisplayName = %q", p.DisplayName())
	}
	if ac := p.AcceptableAssetClasses(); !ac[db.AssetClassStock] || !ac[db.AssetClassETF] || !ac[db.AssetClassOption] || !ac[db.AssetClassFX] {
		t.Errorf("AcceptableAssetClasses = %v", ac)
	}
	if cu := p.AcceptableCurrencies(); !cu["USD"] || len(cu) != 1 {
		t.Errorf("AcceptableCurrencies = %v", cu)
	}
	if ex := p.AcceptableExchanges(); ex != nil {
		t.Errorf("AcceptableExchanges should be nil, got %v", ex)
	}
	types := p.SupportedIdentifierTypes()
	if len(types) != 5 || types[0] != "SEGMENT_MIC_TICKER" || types[1] != "MIC_TICKER" || types[2] != "OPENFIGI_TICKER" || types[3] != "OCC" || types[4] != "FX_PAIR" {
		t.Errorf("SupportedIdentifierTypes = %v", types)
	}
}

func TestFetchPrices_FX_GBXUSD(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		resp := client.APIResponse[[]client.AggBar]{
			Status:  "OK",
			Results: []client.AggBar{{O: 1.25, H: 1.27, L: 1.24, C: 1.26, V: 0, T: 1704067200000}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBXUSD"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassFX, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if len(result.Bars) != 1 {
		t.Fatalf("expected 1 bar, got %d", len(result.Bars))
	}
	// Verify GBXUSD was rewritten to GBPUSD for the API call.
	expected := "/v2/aggs/ticker/C:GBPUSD/range/1/day/2024-01-01/2024-01-01"
	if requestedPath != expected {
		t.Errorf("path = %q, want %q", requestedPath, expected)
	}
	// Verify prices are divided by 100.
	if result.Bars[0].Close != 0.0126 {
		t.Errorf("bar[0].Close = %v, want 0.0126", result.Bars[0].Close)
	}
	if result.Bars[0].Open == nil || *result.Bars[0].Open != 0.0125 {
		t.Errorf("bar[0].Open = %v, want 0.0125", result.Bars[0].Open)
	}
}

func TestFetchPrices_EmptyBars_TickerNotFound_ReturnsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v2/aggs/") {
			// Aggs endpoint: 200 OK with 0 results (like Polygon does for unknown tickers).
			resp := client.APIResponse[[]client.AggBar]{Status: "OK", Results: nil}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "/v3/reference/tickers/") {
			// Reference endpoint: 404 not found.
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "RHM"}}
	from := time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 22, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent when ticker not found, got %v", err)
	}
}

func TestFetchPrices_EmptyBars_TickerExists_ReturnsNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v2/aggs/") {
			resp := client.APIResponse[[]client.AggBar]{Status: "OK", Results: nil}
			json.NewEncoder(w).Encode(resp)
			return
		}
		if strings.Contains(r.URL.Path, "/v3/reference/tickers/") {
			// Reference endpoint: ticker exists.
			resp := client.APIResponse[*client.TickerOverviewResult]{
				Status:  "OK",
				Results: &client.TickerOverviewResult{Ticker: "AAPL", Name: "Apple Inc"},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	from := time.Date(2025, 7, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 7, 8, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	if err != pricefetcher.ErrNoData {
		t.Errorf("expected ErrNoData when ticker exists but no bars, got %v", err)
	}
}

func TestFetchPrices_ChunksLargeRanges(t *testing.T) {
	var requestCount int
	var requestedPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		requestedPaths = append(requestedPaths, r.URL.Path)
		resp := client.APIResponse[[]client.AggBar]{
			Status:  "OK",
			Results: []client.AggBar{{O: 1.25, H: 1.27, L: 1.24, C: 1.26, V: 0, T: 1704067200000}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBPUSD"}}
	// 400-day range should require at least 2 chunks (maxChunkDays=200).
	from := time.Date(2023, 8, 8, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 9, 12, 0, 0, 0, 0, time.UTC) // exclusive

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassFX, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if requestCount < 2 {
		t.Errorf("expected at least 2 requests for 400-day range, got %d", requestCount)
	}
	if len(result.Bars) != requestCount {
		t.Errorf("expected %d bars (one per chunk), got %d", requestCount, len(result.Bars))
	}
	// Verify first chunk starts at the from date.
	if len(requestedPaths) > 0 && !strings.Contains(requestedPaths[0], "2023-08-08") {
		t.Errorf("first chunk should start at 2023-08-08, got path %q", requestedPaths[0])
	}
	// Verify last chunk ends at the to-1 date.
	if n := len(requestedPaths); n > 0 && !strings.Contains(requestedPaths[n-1], "2024-09-11") {
		t.Errorf("last chunk should end at 2024-09-11, got path %q", requestedPaths[n-1])
	}
}

func TestTickerForAssetClass(t *testing.T) {
	tests := []struct {
		name        string
		ids         []pricefetcher.Identifier
		assetClass  string
		wantTicker  string
		wantDivisor float64
	}{
		{"stock_ticker", []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, db.AssetClassStock, "AAPL", 1},
		{"option_occ", []pricefetcher.Identifier{{Type: "OCC", Value: "AAPL250321C00150000"}}, db.AssetClassOption, "O:AAPL250321C00150000", 1},
		{"fx_pair", []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "EURUSD"}}, db.AssetClassFX, "C:EURUSD", 1},
		{"fx_gbxusd", []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBXUSD"}}, db.AssetClassFX, "C:GBPUSD", 100},
		{"fx_no_match", []pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "EURUSD"}}, db.AssetClassFX, "", 1},
		{"stock_no_match", []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "EURUSD"}}, db.AssetClassStock, "", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ticker, divisor := tickerForAssetClass(tc.ids, tc.assetClass)
			if ticker != tc.wantTicker {
				t.Errorf("ticker = %q, want %q", ticker, tc.wantTicker)
			}
			if divisor != tc.wantDivisor {
				t.Errorf("divisor = %v, want %v", divisor, tc.wantDivisor)
			}
		})
	}
}
