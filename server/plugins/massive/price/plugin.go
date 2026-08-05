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
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/shopspring/decimal"
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
	return []string{"SEGMENT_MIC_TICKER", "MIC_TICKER", "OPENFIGI_TICKER", "OCC", "FX_PAIR"}
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

// AcceptableCurrencies returns {"USD": true}. FX pair instruments must have
// currency = 'USD' (the quote currency) in the instruments table for this
// filter to pass them through to FetchPrices.
func (p *Plugin) AcceptableCurrencies() map[string]bool {
	return map[string]bool{"USD": true}
}

// maxChunkDays is the maximum number of calendar days per request. The Polygon
// API caps daily bar responses at ~250 results; 200 keeps us safely under.
const maxChunkDays = 200

func (p *Plugin) FetchPrices(ctx context.Context, config []byte, identifiers []pricefetcher.Identifier, assetClass string, from, before time.Time) (*pricefetcher.FetchResult, error) {
	ticker, fxExp := tickerForAssetClass(identifiers, assetClass)
	if ticker == "" {
		return nil, pricefetcher.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	// Our upper bound is exclusive; the Massive API is inclusive, so subtract one day.
	beforeInclusive := before.AddDate(0, 0, -1)
	if beforeInclusive.Before(from) {
		return nil, pricefetcher.ErrNoData
	}

	var allBars []client.AggBar
	chunkStart := from
	for chunkStart.Before(beforeInclusive) || chunkStart.Equal(beforeInclusive) {
		chunkEnd := chunkStart.AddDate(0, 0, maxChunkDays-1)
		if chunkEnd.After(beforeInclusive) {
			chunkEnd = beforeInclusive
		}
		fromStr := chunkStart.Format("2006-01-02")
		toStr := chunkEnd.Format("2006-01-02")

		bars, err := c.DailyBars(ctx, ticker, fromStr, toStr)
		if err != nil {
			p.reportOutcome(ctx, err)
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
		allBars = append(allBars, bars...)
		chunkStart = chunkEnd.AddDate(0, 0, 1)
	}
	p.reportOutcome(ctx, nil)

	if len(allBars) == 0 {
		// For stocks/ETFs, check whether the ticker exists at all. The aggs
		// endpoint returns 200 with 0 results for unknown tickers, so we
		// probe the reference endpoint to distinguish "no data yet" from
		// "ticker not carried". FX and options use prefixed tickers (C:, O:)
		// that don't work with the reference endpoint.
		if assetClass != db.AssetClassFX && assetClass != db.AssetClassOption {
			if _, err := c.TickerOverview(ctx, ticker); err != nil {
				var nf *client.ErrNotFound
				if errors.As(err, &nf) {
					return nil, &pricefetcher.ErrPermanent{Reason: "ticker not found: " + ticker}
				}
				// Transient errors (rate limit, network) — propagate so the
				// price fetcher retries rather than silently returning ErrNoData.
				p.reportOutcome(ctx, err)
				return nil, err
			}
		}
		return nil, pricefetcher.ErrNoData
	}

	result := make([]pricefetcher.DailyBar, len(allBars))
	for i, b := range allBars {
		// The provider decodes JSON floats, so this is the seam where a
		// provider value becomes exact. NewFromFloat takes the shortest
		// representation that round-trips, so 123.45 stays 123.45.
		o := decimal.NewFromFloat(b.O)
		h := decimal.NewFromFloat(b.H)
		l := decimal.NewFromFloat(b.L)
		v := int64(b.V)
		result[i] = pricefetcher.DailyBar{
			Date:   time.UnixMilli(b.T).UTC().Truncate(24 * time.Hour),
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  decimal.NewFromFloat(b.C),
			Volume: &v,
		}
	}
	if fxExp != 0 {
		result = pricefetcher.ScaleBars(result, fxExp)
	}
	return &pricefetcher.FetchResult{Bars: result}, nil
}

// tickerForAssetClass picks the appropriate ticker from identifiers.
// For FX pairs it also returns the power of ten to shift a derived pair's rates
// by (e.g. GBXUSD); for all other cases the exponent is 0.
func tickerForAssetClass(ids []pricefetcher.Identifier, assetClass string) (string, int32) {
	if assetClass == db.AssetClassOption {
		for _, id := range ids {
			if id.Type == "OCC" && id.Value != "" {
				return "O:" + id.Value, 0
			}
		}
		return "", 0
	}
	if assetClass == db.AssetClassFX {
		for _, id := range ids {
			if id.Type == "FX_PAIR" && id.Value != "" {
				source, exp := pricefetcher.RewriteFXPair(id.Value)
				return "C:" + source, exp
			}
		}
		return "", 0
	}
	// Prefer provider-specific SEGMENT_MIC_TICKER over canonical MIC_TICKER.
	for _, id := range ids {
		if id.Type == "SEGMENT_MIC_TICKER" && id.Value != "" {
			return id.Value, 0
		}
	}
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value != "" {
			return id.Value, 0
		}
	}
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" && id.Value != "" {
			return id.Value, 0
		}
	}
	return "", 0
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
	var rl *client.ErrRateLimit
	switch {
	case err == nil:
		p.counter.Incr(ctx, counterSucceeded)
	case errors.As(err, &rl):
		p.counter.Incr(ctx, counterRateLimit)
	default:
		p.counter.Incr(ctx, counterFailed)
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
