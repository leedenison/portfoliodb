package inflation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://www.ons.gov.uk"

// MonthEntry is one month's data from the ONS timeseries JSON response.
type MonthEntry struct {
	Date  string `json:"date"`  // e.g. "2024 JAN"
	Value string `json:"value"` // e.g. "131.5"
	Year  string `json:"year"`  // e.g. "2024"
	Month string `json:"month"` // e.g. "January"
}

// TimeseriesResponse is the relevant subset of the ONS timeseries JSON.
type TimeseriesResponse struct {
	Months []MonthEntry `json:"months"`
}

// Client fetches data from the ONS website.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ONS API client. baseURL may be empty to use the default.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// FetchTimeseries fetches the full timeseries for a given series ID and dataset.
// e.g. series="l522", dataset="mm23".
func (c *Client) FetchTimeseries(ctx context.Context, series, dataset string) (*TimeseriesResponse, error) {
	url := fmt.Sprintf("%s/economy/inflationandpriceindices/timeseries/%s/%s/data",
		strings.TrimRight(c.baseURL, "/"), strings.ToLower(series), strings.ToLower(dataset))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("ons: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ons: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ons: %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	var ts TimeseriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&ts); err != nil {
		return nil, fmt.Errorf("ons: decode response: %w", err)
	}
	return &ts, nil
}

// monthNameToNumber maps lowercase month names to time.Month.
var monthNameToNumber = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

// ParseMonthEntries converts ONS month entries to (time, value) pairs.
// Entries with unparseable dates or values are skipped.
func ParseMonthEntries(entries []MonthEntry) ([]time.Time, []float64) {
	var months []time.Time
	var values []float64
	for _, e := range entries {
		year, err := strconv.Atoi(e.Year)
		if err != nil {
			continue
		}
		m, ok := monthNameToNumber[strings.ToLower(e.Month)]
		if !ok {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(e.Value), 64)
		if err != nil {
			continue
		}
		months = append(months, time.Date(year, m, 1, 0, 0, 0, 0, time.UTC))
		values = append(values, val)
	}
	return months, values
}
