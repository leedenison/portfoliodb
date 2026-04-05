package inflation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/inflationfetcher"
)

// sampleONSResponse returns a minimal ONS timeseries response.
func sampleONSResponse() TimeseriesResponse {
	return TimeseriesResponse{
		Months: []MonthEntry{
			{Date: "2023 NOV", Value: "128.0", Year: "2023", Month: "November"},
			{Date: "2023 DEC", Value: "128.5", Year: "2023", Month: "December"},
			{Date: "2024 JAN", Value: "130.5", Year: "2024", Month: "January"},
			{Date: "2024 FEB", Value: "131.0", Year: "2024", Month: "February"},
			{Date: "2024 MAR", Value: "131.5", Year: "2024", Month: "March"},
		},
	}
}

func startTestServer(t *testing.T, resp TimeseriesResponse) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestPlugin_FetchInflation_Basic(t *testing.T) {
	ts := startTestServer(t, sampleONSResponse())
	p := NewPlugin(nil, ts.Client())

	config, _ := json.Marshal(configJSON{Series: "l522", Dataset: "mm23", BaseURL: ts.URL})
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchInflation(context.Background(), config, "GBP", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Indices) != 3 {
		t.Fatalf("expected 3 indices (Jan-Mar 2024), got %d", len(result.Indices))
	}

	// Verify first entry.
	idx := result.Indices[0]
	if idx.Month != from {
		t.Errorf("expected month %v, got %v", from, idx.Month)
	}
	if idx.IndexValue != 130.5 {
		t.Errorf("expected 130.5, got %f", idx.IndexValue)
	}
	if idx.BaseYear != 2015 {
		t.Errorf("expected base year 2015, got %d", idx.BaseYear)
	}
}

func TestPlugin_FetchInflation_DateFiltering(t *testing.T) {
	ts := startTestServer(t, sampleONSResponse())
	p := NewPlugin(nil, ts.Client())

	config, _ := json.Marshal(configJSON{Series: "l522", Dataset: "mm23", BaseURL: ts.URL})
	// Only request Feb 2024.
	from := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	result, err := p.FetchInflation(context.Background(), config, "GBP", from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(result.Indices))
	}
	if result.Indices[0].IndexValue != 131.0 {
		t.Errorf("expected 131.0, got %f", result.Indices[0].IndexValue)
	}
}

func TestPlugin_FetchInflation_WrongCurrency(t *testing.T) {
	p := NewPlugin(nil, nil)

	_, err := p.FetchInflation(context.Background(), nil, "USD", time.Time{}, time.Time{})
	if err != inflationfetcher.ErrNoData {
		t.Fatalf("expected ErrNoData for USD, got %v", err)
	}
}

func TestPlugin_FetchInflation_EmptyResponse(t *testing.T) {
	ts := startTestServer(t, TimeseriesResponse{Months: nil})
	p := NewPlugin(nil, ts.Client())

	config, _ := json.Marshal(configJSON{Series: "l522", Dataset: "mm23", BaseURL: ts.URL})
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)

	_, err := p.FetchInflation(context.Background(), config, "GBP", from, to)
	if err != inflationfetcher.ErrNoData {
		t.Fatalf("expected ErrNoData for empty response, got %v", err)
	}
}

func TestPlugin_FetchInflation_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := NewPlugin(nil, ts.Client())
	config, _ := json.Marshal(configJSON{Series: "l522", Dataset: "mm23", BaseURL: ts.URL})

	_, err := p.FetchInflation(context.Background(), config, "GBP",
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestPlugin_DefaultConfig(t *testing.T) {
	p := NewPlugin(nil, nil)
	config := p.DefaultConfig()

	var cfg configJSON
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatalf("invalid default config JSON: %v", err)
	}
	if cfg.Series != "l522" {
		t.Errorf("expected series l522, got %s", cfg.Series)
	}
	if cfg.Dataset != "mm23" {
		t.Errorf("expected dataset mm23, got %s", cfg.Dataset)
	}
}

func TestPlugin_SupportedCurrencies(t *testing.T) {
	p := NewPlugin(nil, nil)
	currencies := p.SupportedCurrencies()
	if len(currencies) != 1 || currencies[0] != "GBP" {
		t.Errorf("expected [GBP], got %v", currencies)
	}
}

func TestParseMonthEntries(t *testing.T) {
	entries := []MonthEntry{
		{Date: "2024 JAN", Value: "130.5", Year: "2024", Month: "January"},
		{Date: "bad", Value: "nope", Year: "bad", Month: "Invalid"},   // skip
		{Date: "2024 FEB", Value: "131.0", Year: "2024", Month: "February"},
	}
	months, values := ParseMonthEntries(entries)
	if len(months) != 2 || len(values) != 2 {
		t.Fatalf("expected 2 entries (skipping invalid), got %d months, %d values", len(months), len(values))
	}
	if months[0] != time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("unexpected first month: %v", months[0])
	}
	if values[0] != 130.5 {
		t.Errorf("unexpected first value: %f", values[0])
	}
}

func TestParseMonthEntries_AllMonths(t *testing.T) {
	// Verify all 12 month names parse correctly.
	names := []string{
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December",
	}
	var entries []MonthEntry
	for _, name := range names {
		entries = append(entries, MonthEntry{
			Date: "2024 " + name[:3], Value: "100.0", Year: "2024", Month: name,
		})
	}
	months, values := ParseMonthEntries(entries)
	if len(months) != 12 || len(values) != 12 {
		t.Fatalf("expected 12 entries, got %d months, %d values", len(months), len(values))
	}
}
