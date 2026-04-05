// Package inflation implements the inflationfetcher.Plugin interface for the
// UK Office for National Statistics (ONS). It fetches monthly CPI/CPIH index
// values from the ONS timeseries endpoint.
package inflation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/inflationfetcher"
)

// PluginID is the stable plugin_id for registration and plugin_config.
const PluginID = "ons"

// defaultBaseYear is the ONS CPIH base year (2015 = 100).
const defaultBaseYear = 2015

type configJSON struct {
	Series  string `json:"series"`   // ONS series ID, default "l522"
	Dataset string `json:"dataset"`  // ONS dataset, default "mm23"
	BaseURL string `json:"base_url"` // override for testing
}

// Plugin implements inflationfetcher.Plugin using the ONS timeseries API.
type Plugin struct {
	log        *slog.Logger
	httpClient *http.Client

	mu         sync.Mutex
	client     *Client
	lastConfig string
}

// NewPlugin creates an ONS inflation plugin. log and httpClient are optional (nil for tests).
func NewPlugin(log *slog.Logger, httpClient *http.Client) *Plugin {
	return &Plugin{log: log, httpClient: httpClient}
}

func (p *Plugin) DisplayName() string { return "ONS (UK)" }

func (p *Plugin) SupportedCurrencies() []string { return []string{"GBP"} }

func (p *Plugin) DefaultConfig() []byte {
	return []byte(`{"series": "l522", "dataset": "mm23"}`)
}

func (p *Plugin) FetchInflation(ctx context.Context, config []byte, currency string, from, to time.Time) (*inflationfetcher.FetchResult, error) {
	if !strings.EqualFold(currency, "GBP") {
		return nil, inflationfetcher.ErrNoData
	}

	cfg := configJSON{Series: "l522", Dataset: "mm23"}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("ons: parse config: %w", err)
		}
	}
	if cfg.Series == "" {
		cfg.Series = "l522"
	}
	if cfg.Dataset == "" {
		cfg.Dataset = "mm23"
	}

	client := p.getOrCreateClient(cfg)

	ts, err := client.FetchTimeseries(ctx, cfg.Series, cfg.Dataset)
	if err != nil {
		return nil, err
	}

	months, values := ParseMonthEntries(ts.Months)
	if len(months) == 0 {
		return nil, inflationfetcher.ErrNoData
	}

	var indices []inflationfetcher.MonthlyIndex
	for i, m := range months {
		if m.Before(from) || !m.Before(to) {
			continue
		}
		indices = append(indices, inflationfetcher.MonthlyIndex{
			Month:      m,
			IndexValue: values[i],
			BaseYear:   defaultBaseYear,
		})
	}

	if len(indices) == 0 {
		return nil, inflationfetcher.ErrNoData
	}

	return &inflationfetcher.FetchResult{Indices: indices}, nil
}

// getOrCreateClient returns or creates a Client for the given config.
// Recreates the client when config changes (e.g. admin updates base_url).
func (p *Plugin) getOrCreateClient(cfg configJSON) *Client {
	key := cfg.BaseURL

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil && p.lastConfig == key {
		return p.client
	}

	httpClient := p.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	p.client = NewClient(cfg.BaseURL, httpClient)
	p.lastConfig = key
	return p.client
}
