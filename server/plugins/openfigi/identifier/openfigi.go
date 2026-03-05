package identifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const openFIGIBaseURL = "https://api.openfigi.com"

// OpenFIGIClient calls OpenFIGI mapping and search APIs.
type OpenFIGIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewOpenFIGIClient creates a client. apiKey may be empty (lower rate limits).
func NewOpenFIGIClient(apiKey string) *OpenFIGIClient {
	return NewOpenFIGIClientWithBaseURL(apiKey, openFIGIBaseURL)
}

// NewOpenFIGIClientWithBaseURL creates a client with a custom base URL (for testing).
func NewOpenFIGIClientWithBaseURL(apiKey, baseURL string) *OpenFIGIClient {
	if baseURL == "" {
		baseURL = openFIGIBaseURL
	}
	return &OpenFIGIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MappingJob is one job in a mapping request.
type MappingJob struct {
	IDType   string   `json:"idType"`
	IDValue  string   `json:"idValue"`
	ExchCode string   `json:"exchCode,omitempty"`
	Currency string   `json:"currency,omitempty"`
	MICCode  string   `json:"micCode,omitempty"`
}

// MappingResponseItem is one element in the mapping response array (per job).
type MappingResponseItem struct {
	Data    []OpenFIGIResult `json:"data,omitempty"`
	Error  string           `json:"error,omitempty"`
	Warning string           `json:"warning,omitempty"`
}

// OpenFIGIResult is one instrument from OpenFIGI (mapping or search data element).
type OpenFIGIResult struct {
	FIGI                string  `json:"figi"`
	Ticker              string  `json:"ticker"`
	Name                string  `json:"name"`
	ExchCode            string  `json:"exchCode"`
	SecurityType        string  `json:"securityType"`
	SecurityType2       string  `json:"securityType2"`
	MarketSector        string  `json:"marketSector"`
	SecurityDescription string  `json:"securityDescription"`
	ShareClassFIGI      *string `json:"shareClassFIGI"`
	CompositeFIGI       *string `json:"compositeFIGI"`
}

// SearchRequest is the body for POST /v3/search.
type SearchRequest struct {
	Query   string `json:"query,omitempty"`
	Start   string `json:"start,omitempty"` // pagination
	ExchCode string `json:"exchCode,omitempty"`
}

// SearchResponse is the response from POST /v3/search.
type SearchResponse struct {
	Data []OpenFIGIResult `json:"data,omitempty"`
	Next string           `json:"next,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Mapping calls POST /v3/mapping with one job. Returns results or error string from API.
func (c *OpenFIGIClient) Mapping(ctx context.Context, job MappingJob) ([]OpenFIGIResult, error) {
	body, err := json.Marshal([]MappingJob{job})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/mapping", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-OPENFIGI-APIKEY", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("openfigi rate limit (429)")
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openfigi mapping %d: %s", resp.StatusCode, string(slurp))
	}
	var items []MappingResponseItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("openfigi mapping: empty response")
	}
	item := items[0]
	if item.Error != "" {
		return nil, fmt.Errorf("openfigi: %s", item.Error)
	}
	return item.Data, nil
}

// Search calls POST /v3/search. Returns first page of results only.
func (c *OpenFIGIClient) Search(ctx context.Context, query string, exchCode string) (*SearchResponse, error) {
	body := SearchRequest{Query: query}
	if exchCode != "" {
		body.ExchCode = exchCode
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/search", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-OPENFIGI-APIKEY", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("openfigi rate limit (429)")
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openfigi search %d: %s", resp.StatusCode, string(slurp))
	}
	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("openfigi: %s", out.Error)
	}
	return &out, nil
}
