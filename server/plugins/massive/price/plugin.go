package price

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and price_plugin_config.
const PluginID = "massive"

type configJSON struct {
	MassiveAPIKey  string `json:"massive_api_key"`
	MassiveBaseURL string `json:"massive_base_url"`
	CallsPerMin    *int   `json:"massive_calls_per_min"`
}

// Plugin implements pricefetcher.Plugin using the Massive aggregates API.
type Plugin struct {
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client

	mu         sync.Mutex
	client     *client.Client
	lastConfig string
}

// NewPlugin returns a plugin. counter and log are optional (nil for tests).
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger, httpClient *http.Client) *Plugin {
	return &Plugin{counter: counter, log: log, httpClient: httpClient}
}

func (p *Plugin) DisplayName() string { return "Massive" }

func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{}
	out, _ := json.Marshal(cfg)
	return out
}

func (p *Plugin) SupportedIdentifierTypes() []string {
	return []string{"TICKER", "OCC", "FX_PAIR"}
}

func (p *Plugin) AcceptableAssetClasses() map[string]bool {
	return map[string]bool{
		db.AssetClassStock:  true,
		db.AssetClassETF:    true,
		db.AssetClassOption: true,
		db.AssetClassFX:     true,
	}
}

func (p *Plugin) AcceptableExchanges() map[string]bool { return nil }

func (p *Plugin) AcceptableCurrencies() map[string]bool {
	return map[string]bool{"USD": true}
}

func (p *Plugin) FetchPrices(ctx context.Context, config []byte, identifiers []pricefetcher.Identifier, assetClass string, from, to time.Time) (*pricefetcher.FetchResult, error) {
	ticker := tickerForAssetClass(identifiers, assetClass)
	if ticker == "" {
		return nil, pricefetcher.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	fromStr := from.Format("2006-01-02")
	// to is exclusive in our convention; Massive API is inclusive, so subtract one day.
	toStr := to.AddDate(0, 0, -1).Format("2006-01-02")
	if toStr < fromStr {
		return nil, pricefetcher.ErrNoData
	}

	bars, err := c.DailyBars(ctx, ticker, fromStr, toStr)
	p.reportOutcome(ctx, err)
	if err != nil {
		var nf *client.ErrNotFound
		var fb *client.ErrForbidden
		if errors.As(err, &nf) {
			return nil, &pricefetcher.ErrPermanent{Reason: "ticker not found: " + nf.Path}
		}
		if errors.As(err, &fb) {
			return nil, &pricefetcher.ErrPermanent{Reason: "forbidden: " + fb.Message}
		}
		return nil, err
	}
	if len(bars) == 0 {
		return nil, pricefetcher.ErrNoData
	}

	result := make([]pricefetcher.DailyBar, len(bars))
	for i, b := range bars {
		o := b.O
		h := b.H
		l := b.L
		v := int64(b.V)
		result[i] = pricefetcher.DailyBar{
			Date:   time.UnixMilli(b.T).UTC().Truncate(24 * time.Hour),
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  b.C,
			Volume: &v,
		}
	}
	return &pricefetcher.FetchResult{Bars: result}, nil
}

// tickerForAssetClass picks the appropriate ticker from identifiers.
func tickerForAssetClass(ids []pricefetcher.Identifier, assetClass string) string {
	if assetClass == db.AssetClassOption {
		for _, id := range ids {
			if id.Type == "OCC" && id.Value != "" {
				return "O:" + id.Value
			}
		}
		return ""
	}
	if assetClass == db.AssetClassFX {
		for _, id := range ids {
			if id.Type == "FX_PAIR" && id.Value != "" {
				return "C:" + id.Value
			}
		}
		return ""
	}
	for _, id := range ids {
		if id.Type == "TICKER" && id.Value != "" {
			return id.Value
		}
	}
	return ""
}

const (
	counterSucceeded = "prices.fetch.massive.request.succeeded"
	counterFailed    = "prices.fetch.massive.request.failed"
	counterRateLimit = "prices.fetch.massive.request.rate_limit"
)

func (p *Plugin) reportOutcome(ctx context.Context, err error) {
	if p.counter == nil {
		return
	}
	switch {
	case err == nil:
		p.counter.Incr(ctx, counterSucceeded)
	default:
		var rl *client.ErrRateLimit
		if errors.As(err, &rl) {
			p.counter.Incr(ctx, counterRateLimit)
		} else {
			p.counter.Incr(ctx, counterFailed)
		}
	}
}

func (p *Plugin) getClient(config []byte) (*client.Client, error) {
	raw := string(config)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.lastConfig == raw {
		return p.client, nil
	}
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, err
		}
	}
	perMin := 0
	if cfg.CallsPerMin != nil {
		perMin = *cfg.CallsPerMin
	}
	limiter := client.NewRateLimiter(perMin)
	p.client = client.New(cfg.MassiveAPIKey, cfg.MassiveBaseURL, limiter, p.log, p.httpClient)
	p.lastConfig = raw
	return p.client, nil
}
