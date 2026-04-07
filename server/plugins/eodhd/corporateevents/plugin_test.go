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
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
)

func configWithURL(url string) []byte {
	cfg := configJSON{EODHDBaseURL: url}
	b, _ := json.Marshal(cfg)
	return b
}

func TestFetchEvents_StockSplitsAndDividends(t *testing.T) {
	splits := []client.SplitRow{
		{Date: "2020-08-31", Split: "4.000000/1.000000"},
		{Date: "2014-06-09", Split: "7.000000/1.000000"},
	}
	dividends := []client.DividendRow{
		{
			Date:            "2024-02-09",
			DeclarationDate: "2024-02-01",
			RecordDate:      "2024-02-12",
			PaymentDate:     "2024-02-15",
			Period:          "Quarterly",
			Value:           0.24,
			UnadjustedValue: 0.24,
			Currency:        "USD",
		},
	}

	var splitPath, divPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case startsWith(r.URL.Path, "/api/splits/"):
			splitPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(splits)
		case startsWith(r.URL.Path, "/api/div/"):
			divPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(dividends)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exchMap := exchangemap.New()
	p := NewPlugin(nil, nil, srv.Client(), exchMap)
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}

	from := time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	got, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock, from, to)
	if err != nil {
		t.Fatalf("FetchEvents: %v", err)
	}
	if len(got.Splits) != 2 {
		t.Fatalf("expected 2 splits, got %d", len(got.Splits))
	}
	if got.Splits[0].SplitTo != "4" || got.Splits[0].SplitFrom != "1" {
		t.Errorf("first split: %+v", got.Splits[0])
	}
	if got.Splits[0].ExDate != time.Date(2020, 8, 31, 0, 0, 0, 0, time.UTC) {
		t.Errorf("first split date: %v", got.Splits[0].ExDate)
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
	if splitPath != "/api/splits/AAPL.US" {
		t.Errorf("split path: %q", splitPath)
	}
	if divPath != "/api/div/AAPL.US" {
		t.Errorf("div path: %q", divPath)
	}
}

func TestFetchEvents_PrefersUnadjustedValue(t *testing.T) {
	// Provider sends adjusted Value=0.06 (after a 4:1 split) and the
	// real cash paid UnadjustedValue=0.24. We must store the unadjusted
	// value so the dividend amount does not drift.
	dividends := []client.DividendRow{
		{Date: "2020-02-07", Value: 0.06, UnadjustedValue: 0.24, Currency: "USD"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if startsWith(r.URL.Path, "/api/splits/") {
			_ = json.NewEncoder(w).Encode([]client.SplitRow{})
			return
		}
		_ = json.NewEncoder(w).Encode(dividends)
	}))
	defer srv.Close()

	p := NewPlugin(nil, nil, srv.Client(), exchangemap.New())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	got, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2020, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("FetchEvents: %v", err)
	}
	if len(got.CashDividends) != 1 {
		t.Fatalf("expected 1 dividend, got %d", len(got.CashDividends))
	}
	if got.CashDividends[0].Amount != "0.24" {
		t.Errorf("expected unadjusted 0.24, got %s", got.CashDividends[0].Amount)
	}
}

func TestFetchEvents_404IsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := NewPlugin(nil, nil, srv.Client(), exchangemap.New())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "BOGUS"}}
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
	p := NewPlugin(nil, nil, srv.Client(), exchangemap.New())
	ids := []corporateevents.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	_, err := p.FetchEvents(context.Background(), configWithURL(srv.URL), ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	var tr *corporateevents.ErrTransient
	if !errors.As(err, &tr) {
		t.Fatalf("expected ErrTransient, got %v", err)
	}
}

func TestFetchEvents_NoSupportedIdentifier(t *testing.T) {
	p := NewPlugin(nil, nil, nil, exchangemap.New())
	ids := []corporateevents.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	_, err := p.FetchEvents(context.Background(), nil, ids, db.AssetClassStock,
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, corporateevents.ErrNoData) {
		t.Errorf("expected ErrNoData, got %v", err)
	}
}

func TestParseSplit_BadFormat(t *testing.T) {
	if _, ok := parseSplit(client.SplitRow{Date: "2024-01-01", Split: "garbage"}); ok {
		t.Error("expected parse failure for non-fraction")
	}
	if _, ok := parseSplit(client.SplitRow{Date: "not-a-date", Split: "2/1"}); ok {
		t.Error("expected parse failure for bad date")
	}
	if _, ok := parseSplit(client.SplitRow{Date: "2024-01-01", Split: "0/1"}); ok {
		t.Error("expected parse failure for zero split_to")
	}
}

func TestPluginAcceptsAssetClasses(t *testing.T) {
	p := NewPlugin(nil, nil, nil, nil)
	ac := p.AcceptableAssetClasses()
	if !ac[db.AssetClassStock] || !ac[db.AssetClassETF] {
		t.Error("expected STOCK and ETF accepted")
	}
	if ac[db.AssetClassOption] {
		t.Error("OPTION should not be accepted")
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
