package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

const defaultBaseURL = "https://api.massive.com"

// ErrRateLimit is returned when the Massive API responds with 429.
type ErrRateLimit struct{}

func (e *ErrRateLimit) Error() string {
	return "massive rate limit (429)"
}

// ErrForbidden is returned when the Massive API responds with 401 or 403
// (e.g. invalid API key, plan doesn't include the requested data).
type ErrForbidden struct {
	Path    string
	Message string
}

func (e *ErrForbidden) Error() string {
	return fmt.Sprintf("massive %s: forbidden (403): %s", e.Path, e.Message)
}

// ErrNotFound is returned when the Massive API responds with 404.
type ErrNotFound struct {
	Path string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("massive %s: not found (404)", e.Path)
}

// Client calls the Massive REST API with shared rate limiting.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
	log        *slog.Logger
}

// New creates a Client. baseURL may be empty to use the default Massive API URL; pass a custom URL for testing.
// httpClient is the HTTP client used for requests. log is optional (nil allowed).
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
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive request failed", "url", path, "err", err)
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		if c.log != nil {
			c.log.WarnContext(ctx, "massive rate limit (429)", "url", path)
		}
		return &ErrRateLimit{}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		if c.log != nil {
			c.log.WarnContext(ctx, "massive auth error", "url", path, "status", resp.StatusCode, "body", string(body))
		}
		return &ErrForbidden{Path: path, Message: string(body)}
	}
	if resp.StatusCode == http.StatusNotFound {
		if c.log != nil {
			c.log.DebugContext(ctx, "massive not found (404)", "url", path)
		}
		return &ErrNotFound{Path: path}
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive request failed", "url", path, "status", resp.StatusCode, "body", string(body))
		}
		return fmt.Errorf("massive %s %d: %s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		if c.log != nil {
			c.log.ErrorContext(ctx, "massive decode failed", "url", path, "err", err)
		}
		return fmt.Errorf("massive decode %s: %w", path, err)
	}
	if c.log != nil {
		c.log.DebugContext(ctx, "massive request succeeded", "url", path)
	}
	return nil
}
