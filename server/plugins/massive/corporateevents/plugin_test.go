package corporateevents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

func configWithURL(url string) []byte {
	cfg := configJSON{MassiveBaseURL: url, MassiveAPIKey: "test-key"}
	b, _ := json.Marshal(cfg)
	return b
}

// envelope wraps results in the Massive APIResponse shape used by tests.
type envelope struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	NextURL   string `json:"next_url,omitempty"`
	Results   any    `json:"results"`
}

func TestFetchEvents_StockSplitsAndDividends(t *testing.T) {
	splits := []client.SplitResult{
		{Ticker: "AAPL", ExecutionDate: "2020-08-31", SplitFrom: 1, SplitTo: 4},
		{Ticker: "AAPL", ExecutionDate: "2014-06-09", SplitFrom: 1, SplitTo: 7},
	}
	dividends := []client.DividendResult{
		{
			Ticker:          "AAPL",
			ExDividendDate:  "2024-02-09",
			DeclarationDate: "2024-02-01",
			RecordDate:      "2024-02-12",
			PayDate:         "2024-02-15",
			CashAmount:      0.24,
			Currency:        "USD",
			Frequency:       4,
		},
	}

	var splitTicker, divTicker string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/reference/splits":
			splitTicker = r.URL.Query().Get("ticker")
			_ = json.NewEncoder(w).Encode(envelope{Status: "OK", Results: splits})
		case "/v3/reference/dividends":
			divTicker = r.URL.Query().Get("ticker")
			_ = json.NewEncoder(w).Encode(envelope{Status: "OK", Results: dividends})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	got, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("FetchEvents: %v", err)
	}
	if splitTicker != "AAPL" || divTicker != "AAPL" {
		t.Errorf("ticker filter not sent: split=%q div=%q", splitTicker, divTicker)
	}
	if len(got.Splits) != 2 {
		t.Fatalf("expected 2 splits, got %d", len(got.Splits))
	}
	if got.Splits[0].SplitTo != "4" || got.Splits[0].SplitFrom != "1" {
		t.Errorf("first split: %+v", got.Splits[0])
	}
	if len(got.CashDividends) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got.CashDividends))
	}
	d0 := got.CashDividends[0]
	if d0.Amount != "0.24" || d0.Currency != "USD" || d0.Frequency != "quarterly" {
		t.Errorf("dividend: %+v", d0)
	}
	if d0.PayDate != time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("pay date: %v", d0.PayDate)
	}
}

func TestFetchEvents_PaginationFollowsNextURL(t *testing.T) {
	page1 := []client.SplitResult{{Ticker: "AAPL", ExecutionDate: "2014-06-09", SplitFrom: 1, SplitTo: 7}}
	page2 := []client.SplitResult{{Ticker: "AAPL", ExecutionDate: "2020-08-31", SplitFrom: 1, SplitTo: 4}}

	var calls int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v3/reference/dividends" {
			_ = json.NewEncoder(w).Encode(envelope{Status: "OK", Results: []client.DividendResult{}})
			return
		}
		calls++
		if r.URL.Query().Get("cursor") == "" {
			// First page returns a next_url that the client must follow.
			next := srv.URL + "/v3/reference/splits?cursor=PAGE2"
			_ = json.NewEncoder(w).Encode(envelope{Status: "OK", NextURL: next, Results: page1})
			return
		}
		_ = json.NewEncoder(w).Encode(envelope{Status: "OK", Results: page2})
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	got, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("FetchEvents: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 splits page calls, got %d", calls)
	}
	if len(got.Splits) != 2 {
		t.Fatalf("expected 2 splits across both pages, got %d", len(got.Splits))
	}
}

func TestFetchEvents_404IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := NewPlugin(nil, nil, srv.Client())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "BOGUS"}}
	_, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	var perm *corporateevents.ErrPermanent
	if !errors.As(err, &perm) {
		t.Fatalf("expected ErrPermanent, got %v", err)
	}
}

func TestFetchEvents_403IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"plan does not include corporate actions"}`))
	}))
	defer srv.Close()
	p := NewPlugin(nil, nil, srv.Client())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	_, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	var perm *corporateevents.ErrPermanent
	if !errors.As(err, &perm) {
		t.Fatalf("expected ErrPermanent, got %v", err)
	}
}

func TestFetchEvents_429IsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := NewPlugin(nil, nil, srv.Client())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	_, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	var tr *corporateevents.ErrTransient
	if !errors.As(err, &tr) {
		t.Fatalf("expected ErrTransient, got %v", err)
	}
}

func TestFetchEvents_NoSupportedIdentifier(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	ids := []corporateevents.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	_, err := p.FetchEvents(context.Background(), nil, ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, corporateevents.ErrNoData) {
		t.Errorf("expected ErrNoData, got %v", err)
	}
}

func TestFrequencyFromInt(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{1, "annual"},
		{2, "semi-annual"},
		{4, "quarterly"},
		{12, "monthly"},
		{0, ""},
		{99, ""},
	}
	for _, c := range cases {
		if got := frequencyFromInt(c.in); got != c.want {
			t.Errorf("frequencyFromInt(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPluginAcceptsAssetClasses(t *testing.T) {
	p := NewPlugin(nil, nil, nil)
	ac := p.AcceptableAssetClasses()
	if !ac[db.AssetClassStock] || !ac[db.AssetClassETF] {
		t.Error("expected STOCK and ETF accepted")
	}
	if ac[db.AssetClassOption] {
		t.Error("OPTION should not be accepted")
	}
}
