package client

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
)

// Config is the plugin config JSON every EODHD plugin reads. The three plugins
// -- identifier, price and corporate event -- are configured independently in
// the admin UI but share a config vocabulary, because they share the account
// whose key and rate limit it describes.
type Config struct {
	APIKey      string `json:"eodhd_api_key"`
	BaseURL     string `json:"eodhd_base_url"` // for testing
	CallsPerMin *int   `json:"eodhd_calls_per_min"`
}

// ParseConfig reads a Config from plugin config JSON. Empty config is the zero
// Config rather than an error: a plugin enabled before its key is entered is
// configured, just not usefully, and the API call is what reports that.
func ParseConfig(config []byte) (Config, error) {
	var c Config
	if len(config) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(config, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Cache holds the Client a plugin built from its config and rebuilds it only
// when the config changes, so a rate limiter is not discarded between calls.
//
// The zero Cache is ready to use. Safe for concurrent use.
type Cache struct {
	mu         sync.Mutex
	client     *Client
	config     Config
	lastConfig string
}

// Get returns the client for config along with the Config it was built from,
// rebuilding both when config differs from the previous call.
func (c *Cache) Get(config []byte, log *slog.Logger, httpClient *http.Client) (*Client, Config, error) {
	raw := string(config)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && c.lastConfig == raw {
		return c.client, c.config, nil
	}
	cfg, err := ParseConfig(config)
	if err != nil {
		return nil, Config{}, err
	}
	perMin := 0
	if cfg.CallsPerMin != nil {
		perMin = *cfg.CallsPerMin
	}
	c.client = New(cfg.APIKey, cfg.BaseURL, NewRateLimiter(perMin), log, httpClient)
	c.config = cfg
	c.lastConfig = raw
	return c.client, c.config, nil
}
