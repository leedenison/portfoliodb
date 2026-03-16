package identifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/leedenison/portfoliodb/server/telemetry"
)

const openFIGIBaseURL = "https://api.openfigi.com"

// OpenFIGIClient calls OpenFIGI mapping and search APIs.
type OpenFIGIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
}

// NewOpenFIGIClient creates a client. apiKey may be empty (lower rate limits).
// baseURL may be empty to use the default OpenFIGI API URL; pass a custom URL for testing.
// counter and log are optional (nil allowed) for metrics and logging.
func NewOpenFIGIClient(apiKey, baseURL string, counter telemetry.CounterIncrementer, log *slog.Logger, httpClient *http.Client) *OpenFIGIClient {
	if baseURL == "" {
		baseURL = openFIGIBaseURL
	}
	return &OpenFIGIClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: httpClient,
		counter:    counter,
		log:        log,
	}
}

// MappingJob is one job in a mapping request.
type MappingJob struct {
	IDType        string `json:"idType"`
	IDValue       string `json:"idValue"`
	ExchCode      string `json:"exchCode,omitempty"`
	Currency      string `json:"currency,omitempty"`
	MICCode       string `json:"micCode,omitempty"`
	SecurityType2 string `json:"securityType2,omitempty"`
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
	if c.counter != nil {
		c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.attempts")
	}
	if c.log != nil {
		c.log.DebugContext(ctx, "OpenFIGI mapping", "idType", job.IDType, "idValue", job.IDValue, "exchCode", job.ExchCode)
	}
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
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI mapping request failed", "err", err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.rate_limit")
		}
		if c.log != nil {
			c.log.WarnContext(ctx, "OpenFIGI mapping rate limit (429)", "url", req.URL.String())
		}
		return nil, fmt.Errorf("openfigi rate limit (429)")
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.failed")
		}
		if c.log != nil {
			args := []any{"status", resp.StatusCode, "body", string(slurp)}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				args = append(args, "url", req.URL.String())
			}
			c.log.ErrorContext(ctx, "OpenFIGI mapping failed", args...)
		}
		return nil, fmt.Errorf("openfigi mapping %d: %s", resp.StatusCode, string(slurp))
	}
	var items []MappingResponseItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI mapping decode failed", "err", err)
		}
		return nil, err
	}
	if len(items) == 0 {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI mapping empty response")
		}
		return nil, fmt.Errorf("openfigi mapping: empty response")
	}
	item := items[0]
	if item.Error != "" {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI mapping API error", "error", item.Error)
		}
		return nil, fmt.Errorf("openfigi: %s", item.Error)
	}
	if c.counter != nil {
		if len(item.Data) == 0 {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.zero_results")
		} else {
			c.counter.Incr(ctx, "instruments.identification.openfigi.mapping.succeeded")
		}
	}
	if c.log != nil {
		if len(item.Data) > 0 {
			c.log.DebugContext(ctx, "OpenFIGI mapping succeeded", "results", len(item.Data))
		} else {
			c.log.DebugContext(ctx, "OpenFIGI mapping returned no results")
		}
	}
	return item.Data, nil
}

// Search calls POST /v3/search. Returns first page of results only.
func (c *OpenFIGIClient) Search(ctx context.Context, query string, exchCode string) (*SearchResponse, error) {
	if c.counter != nil {
		c.counter.Incr(ctx, "instruments.identification.openfigi.search.attempts")
	}
	if c.log != nil {
		c.log.DebugContext(ctx, "OpenFIGI search", "query", query, "exchCode", exchCode)
	}
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
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI search request failed", "err", err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.rate_limit")
		}
		if c.log != nil {
			c.log.WarnContext(ctx, "OpenFIGI search rate limit (429)", "url", req.URL.String())
		}
		return nil, fmt.Errorf("openfigi rate limit (429)")
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(resp.Body)
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.failed")
		}
		if c.log != nil {
			args := []any{"status", resp.StatusCode, "body", string(slurp)}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				args = append(args, "url", req.URL.String())
			}
			c.log.ErrorContext(ctx, "OpenFIGI search failed", args...)
		}
		return nil, fmt.Errorf("openfigi search %d: %s", resp.StatusCode, string(slurp))
	}
	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI search decode failed", "err", err)
		}
		return nil, err
	}
	if out.Error != "" {
		if c.counter != nil {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.failed")
		}
		if c.log != nil {
			c.log.ErrorContext(ctx, "OpenFIGI search API error", "error", out.Error)
		}
		return nil, fmt.Errorf("openfigi: %s", out.Error)
	}
	if c.counter != nil {
		if len(out.Data) == 0 {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.zero_results")
		} else {
			c.counter.Incr(ctx, "instruments.identification.openfigi.search.succeeded")
		}
	}
	if c.log != nil {
		c.log.DebugContext(ctx, "OpenFIGI search succeeded", "results", len(out.Data))
	}
	return &out, nil
}
