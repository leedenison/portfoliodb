package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/leedenison/portfoliodb/server/telemetry"
)

const defaultBaseURL = "https://api.massive.com"

// ErrRateLimit is returned when the Massive API responds with 429.
type ErrRateLimit struct{}

func (e *ErrRateLimit) Error() string {
	return "massive rate limit (429)"
}

// Client calls the Massive REST API with shared rate limiting.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
}

// New creates a Client. baseURL may be empty to use the default Massive API URL; pass a custom URL for testing.
// counter and log are optional (nil allowed).
func New(apiKey, baseURL string, limiter *RateLimiter, counter telemetry.CounterIncrementer, log *slog.Logger) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		limiter: limiter,
		counter: counter,
		log:     log,
	}
}

// get issues a rate-limited GET request and decodes the JSON response into out.
func (c *Client) get(ctx context.Context, path string, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("massive rate limiter: %w", err)
	}
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	q := req.URL.Query()
	q.Set("apiKey", c.apiKey)
	req.URL.RawQuery = q.Encode()

	if c.log != nil {
		c.log.DebugContext(ctx, "massive GET", "url", path)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.incr(ctx, "instruments.identification.massive.request.failed")
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive request failed", "url", path, "err", err)
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		c.incr(ctx, "instruments.identification.massive.request.rate_limit")
		if c.log != nil {
			c.log.WarnContext(ctx, "massive rate limit (429)", "url", path)
		}
		return &ErrRateLimit{}
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.incr(ctx, "instruments.identification.massive.request.failed")
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive request failed", "url", path, "status", resp.StatusCode, "body", string(body))
		}
		return fmt.Errorf("massive %s %d: %s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		c.incr(ctx, "instruments.identification.massive.request.failed")
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive decode failed", "url", path, "err", err)
		}
		return fmt.Errorf("massive decode %s: %w", path, err)
	}
	c.incr(ctx, "instruments.identification.massive.request.succeeded")
	if c.log != nil {
		c.log.DebugContext(ctx, "massive request succeeded", "url", path)
	}
	return nil
}

func (c *Client) incr(ctx context.Context, name string) {
	if c.counter != nil {
		c.counter.Incr(ctx, name)
	}
}
