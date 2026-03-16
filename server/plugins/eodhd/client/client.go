package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

const defaultBaseURL = "https://eodhd.com"

// ErrRateLimit is returned when the EODHD API responds with 429.
type ErrRateLimit struct{}

func (e *ErrRateLimit) Error() string {
	return "eodhd rate limit (429)"
}

// ErrNotFound is returned when the EODHD API responds with 404.
type ErrNotFound struct {
	Path string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("eodhd %s: not found (404)", e.Path)
}

// Client calls the EODHD REST API with shared rate limiting.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
	log        *slog.Logger
}

// New creates a Client. baseURL may be empty to use the default EODHD URL.
func New(apiKey, baseURL string, limiter *RateLimiter, log *slog.Logger, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: httpClient,
		limiter:    limiter,
		log:        log,
	}
}

// SearchOption configures optional query parameters for the Search API.
type SearchOption func(q url.Values)

// WithExchange filters search results to a specific exchange code.
func WithExchange(exchange string) SearchOption {
	return func(q url.Values) {
		if exchange != "" {
			q.Set("exchange", exchange)
		}
	}
}

// WithLimit sets the maximum number of search results.
func WithLimit(n int) SearchOption {
	return func(q url.Values) {
		if n > 0 {
			q.Set("limit", fmt.Sprintf("%d", n))
		}
	}
}

// Search calls the EODHD Search API to find instruments matching query.
func (c *Client) Search(ctx context.Context, query string, opts ...SearchOption) ([]SearchResult, error) {
	path := "/api/search/" + url.PathEscape(query)
	q := url.Values{}
	q.Set("fmt", "json")
	q.Set("type", "stock")
	for _, opt := range opts {
		opt(q)
	}
	var results []SearchResult
	if err := c.get(ctx, path, q, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// Fundamentals calls the EODHD Fundamentals API for the General section.
func (c *Client) Fundamentals(ctx context.Context, code, exchange string) (*FundamentalsGeneral, error) {
	path := fmt.Sprintf("/api/fundamentals/%s.%s", url.PathEscape(code), url.PathEscape(exchange))
	q := url.Values{}
	q.Set("fmt", "json")
	q.Set("filter", "General")
	var result FundamentalsGeneral
	if err := c.get(ctx, path, q, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// get issues a rate-limited GET request and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, extra url.Values, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("eodhd rate limiter: %w", err)
	}
	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	q.Set("api_token", c.apiKey)
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	req.URL.RawQuery = q.Encode()

	if c.log != nil {
		c.log.DebugContext(ctx, "eodhd GET", "url", path)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if c.log != nil {
			c.log.ErrorContext(ctx, "eodhd request failed", "url", path, "err", err)
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		if c.log != nil {
			c.log.WarnContext(ctx, "eodhd rate limit (429)", "url", path)
		}
		return &ErrRateLimit{}
	}
	if resp.StatusCode == http.StatusNotFound {
		if c.log != nil {
			c.log.DebugContext(ctx, "eodhd not found (404)", "url", path)
		}
		return &ErrNotFound{Path: path}
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if c.log != nil {
			c.log.ErrorContext(ctx, "eodhd request failed", "url", path, "status", resp.StatusCode, "body", string(body))
		}
		return fmt.Errorf("eodhd %s %d: %s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		if c.log != nil {
			c.log.ErrorContext(ctx, "eodhd decode failed", "url", path, "err", err)
		}
		return fmt.Errorf("eodhd decode %s: %w", path, err)
	}
	if c.log != nil {
		c.log.DebugContext(ctx, "eodhd request succeeded", "url", path)
	}
	return nil
}
