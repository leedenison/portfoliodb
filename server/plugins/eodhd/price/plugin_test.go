package price

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
)

func configWithURL(url string) []byte {
	cfg := configJSON{EODHDBaseURL: url}
	b, _ := json.Marshal(cfg)
	return b
}

func TestFetchPrices_Stock(t *testing.T) {
	var requestedPath string
	bars := []client.EODBar{
		{Date: "2024-01-02", Open: 100, High: 105, Low: 99, Close: 103, Volume: 1000},
		{Date: "2024-01-03", Open: 103, High: 107, Low: 102, Close: 106, Volume: 1200},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bars)
	}))
	defer srv.Close()

	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, srv.Client(), exchMap)
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "MCD"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)

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
	// Verify the path contains {ticker}.{exchange_code}
	expected := "/api/eod/MCD.US"
	if requestedPath != expected {
		t.Errorf("path = %q, want %q", requestedPath, expected)
	}
}

func TestFetchPrices_FX(t *testing.T) {
	var requestedPath string
	bars := []client.EODBar{
		{Date: "2024-01-02", Open: 1.07, High: 1.09, Low: 1.06, Close: 1.08, Volume: 0},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bars)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client(), nil)
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
	expected := "/api/eod/EURUSD.FOREX"
	if requestedPath != expected {
		t.Errorf("path = %q, want %q", requestedPath, expected)
	}
}

func TestFetchPrices_FX_GBXUSD(t *testing.T) {
	var requestedPath string
	bars := []client.EODBar{
		{Date: "2024-01-02", Open: 1.25, High: 1.27, Low: 1.24, Close: 1.26, Volume: 0},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bars)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client(), nil)
	ids := []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBXUSD"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassFX, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	// Verify GBXUSD was rewritten to GBPUSD for the API call.
	expected := "/api/eod/GBPUSD.FOREX"
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

func TestFetchPrices_NoMatchingIdentifier(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient, nil)
	ids := []pricefetcher.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), nil, ids, db.AssetClassStock, from, to)
	if err != pricefetcher.ErrNoData {
		t.Errorf("expected ErrNoData, got %v", err)
	}
}

func TestFetchPrices_NoExchangeMap(t *testing.T) {
	p := NewPlugin(nil, nil, http.DefaultClient, nil)
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), nil, ids, db.AssetClassStock, from, to)
	if err != pricefetcher.ErrNoData {
		t.Errorf("expected ErrNoData (no exchMap), got %v", err)
	}
}

func TestFetchPrices_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, srv.Client(), exchMap)
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "INVALID"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent on 404, got %v", err)
	}
}

func TestFetchPrices_SubscriptionLimit(t *testing.T) {
	bars := []client.EODBar{
		{Date: "2025-04-06", Open: 1.29, High: 1.29, Low: 1.28, Close: 1.29, Volume: 0,
			Warning: "Data is limited by one year as you have free subscription"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bars)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client(), nil)
	ids := []pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBPUSD"}}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 4, 7, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassFX, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Fatalf("expected ErrPermanent, got %v", err)
	}
	if permErr.Reason == "" {
		t.Error("ErrPermanent.Reason should not be empty")
	}
}

func TestFetchPrices_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, srv.Client(), exchMap)
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"}}
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

func TestFetchPrices_LargeRange(t *testing.T) {
	var requestCount atomic.Int32
	bars := []client.EODBar{
		{Date: "2024-01-02", Open: 100, High: 105, Low: 99, Close: 103, Volume: 1000},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bars)
	}))
	defer srv.Close()

	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, srv.Client(), exchMap)
	ids := []pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"}}
	// Range spanning 400 days: should require 2 chunks.
	from := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 2, 5, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	if err != nil {
		t.Fatalf("FetchPrices: %v", err)
	}
	if requestCount.Load() != 2 {
		t.Errorf("expected 2 API requests (chunked), got %d", requestCount.Load())
	}
	// Each chunk returns 1 bar, so 2 total.
	if len(result.Bars) != 2 {
		t.Errorf("expected 2 bars, got %d", len(result.Bars))
	}
}

func TestSymbolForAssetClass(t *testing.T) {
	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, nil, exchMap)

	tests := []struct {
		name        string
		ids         []pricefetcher.Identifier
		assetClass  string
		wantSymbol  string
		wantDivisor float64
	}{
		{
			"stock_mic_ticker",
			[]pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "MCD"}},
			db.AssetClassStock, "MCD.US", 1,
		},
		{
			"stock_openfigi_ticker",
			[]pricefetcher.Identifier{{Type: "OPENFIGI_TICKER", Domain: "XLON", Value: "SHEL"}},
			db.AssetClassStock, "SHEL.LSE", 1,
		},
		{
			"fx_pair",
			[]pricefetcher.Identifier{{Type: "FX_PAIR", Value: "EURUSD"}},
			db.AssetClassFX, "EURUSD.FOREX", 1,
		},
		{
			"fx_gbxusd",
			[]pricefetcher.Identifier{{Type: "FX_PAIR", Value: "GBXUSD"}},
			db.AssetClassFX, "GBPUSD.FOREX", 100,
		},
		{
			"fx_no_match",
			[]pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"}},
			db.AssetClassFX, "", 1,
		},
		{
			"stock_no_domain",
			[]pricefetcher.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
			db.AssetClassStock, "", 1,
		},
		{
			"stock_unknown_mic",
			[]pricefetcher.Identifier{{Type: "MIC_TICKER", Domain: "ZZZZ", Value: "AAPL"}},
			db.AssetClassStock, "", 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			symbol, divisor := p.symbolForAssetClass(tc.ids, tc.assetClass)
			if symbol != tc.wantSymbol {
				t.Errorf("symbol = %q, want %q", symbol, tc.wantSymbol)
			}
			if divisor != tc.wantDivisor {
				t.Errorf("divisor = %v, want %v", divisor, tc.wantDivisor)
			}
		})
	}
}

func TestPluginInterface(t *testing.T) {
	p := NewPlugin(nil, nil, nil, nil)
	if p.DisplayName() != "EODHD" {
		t.Errorf("DisplayName = %q", p.DisplayName())
	}
	ac := p.AcceptableAssetClasses()
	if !ac[db.AssetClassStock] || !ac[db.AssetClassETF] || !ac[db.AssetClassFX] {
		t.Errorf("AcceptableAssetClasses = %v", ac)
	}
	if len(ac) != 3 {
		t.Errorf("AcceptableAssetClasses len = %d, want 3", len(ac))
	}
	if cu := p.AcceptableCurrencies(); cu != nil {
		t.Errorf("AcceptableCurrencies should be nil, got %v", cu)
	}
	if ex := p.AcceptableExchanges(); ex != nil {
		t.Errorf("AcceptableExchanges should be nil, got %v", ex)
	}
	types := p.SupportedIdentifierTypes()
	if len(types) != 4 || types[0] != "EODHD_EXCH_CODE" || types[1] != "MIC_TICKER" || types[2] != "OPENFIGI_TICKER" || types[3] != "FX_PAIR" {
		t.Errorf("SupportedIdentifierTypes = %v", types)
	}
}
