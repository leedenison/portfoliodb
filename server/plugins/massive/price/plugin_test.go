package price

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	ids := []pricefetcher.Identifier{{Type: "TICKER", Value: "AAPL"}}
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
	ids := []pricefetcher.Identifier{{Type: "TICKER", Value: "INVALID"}}
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
	ids := []pricefetcher.Identifier{{Type: "TICKER", Value: "AAPL"}}
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchPrices(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	var permErr *pricefetcher.ErrPermanent
	if !errors.As(err, &permErr) {
		t.Errorf("expected ErrPermanent on 403, got %v", err)
	}
}

func TestFetchPrices_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []pricefetcher.Identifier{{Type: "TICKER", Value: "AAPL"}}
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

func TestPluginInterface(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	if p.DisplayName() != "Massive" {
		t.Errorf("DisplayName = %q", p.DisplayName())
	}
	if ac := p.AcceptableAssetClasses(); !ac[db.AssetClassStock] || !ac[db.AssetClassETF] || !ac[db.AssetClassOption] {
		t.Errorf("AcceptableAssetClasses = %v", ac)
	}
	if cu := p.AcceptableCurrencies(); !cu["USD"] || len(cu) != 1 {
		t.Errorf("AcceptableCurrencies = %v", cu)
	}
	if ex := p.AcceptableExchanges(); ex != nil {
		t.Errorf("AcceptableExchanges should be nil, got %v", ex)
	}
	types := p.SupportedIdentifierTypes()
	if len(types) != 2 || types[0] != "TICKER" || types[1] != "OCC" {
		t.Errorf("SupportedIdentifierTypes = %v", types)
	}
}
