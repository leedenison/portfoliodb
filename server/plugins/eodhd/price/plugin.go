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
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/shopspring/decimal"
)

// PluginID is the stable plugin_id for registration and plugin_config.
const PluginID = "eodhd"

// maxChunkDays is the maximum date range per EODHD API request.
const maxChunkDays = 365

type configJSON struct {
	EODHDAPIKey  string `json:"eodhd_api_key"`
	EODHDBaseURL string `json:"eodhd_base_url"`
	CallsPerMin  *int   `json:"eodhd_calls_per_min"`
}

// Plugin implements pricefetcher.Plugin using the EODHD EOD API.
type Plugin struct {
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap

	mu         sync.Mutex
	client     *client.Client
	lastConfig string
}

// NewPlugin returns a plugin. counter, log and exchMap are optional (nil for tests).
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger, httpClient *http.Client, exchMap *exchangemap.ExchangeMap) *Plugin {
	return &Plugin{counter: counter, log: log, httpClient: httpClient, exchMap: exchMap}
}

func (p *Plugin) DisplayName() string { return "EODHD" }

func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{}
	out, _ := json.Marshal(cfg)
	return out
}

func (p *Plugin) SupportedIdentifierTypes() []string {
	return []string{"EODHD_EXCH_CODE", "MIC_TICKER", "OPENFIGI_TICKER", "FX_PAIR"}
}

func (p *Plugin) AcceptableAssetClasses() map[string]bool {
	return map[string]bool{
		db.AssetClassStock: true,
		db.AssetClassETF:   true,
		db.AssetClassFX:    true,
	}
}

func (p *Plugin) AcceptableExchanges() map[string]bool { return nil }

func (p *Plugin) AcceptableCurrencies() map[string]bool { return nil }

func (p *Plugin) FetchPrices(ctx context.Context, config []byte, identifiers []pricefetcher.Identifier, assetClass string, from, before time.Time) (*pricefetcher.FetchResult, error) {
	symbol, fxExp := p.symbolForAssetClass(identifiers, assetClass)
	if symbol == "" {
		return nil, pricefetcher.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	// Our upper bound is exclusive; the EODHD API is inclusive.
	beforeInclusive := before.AddDate(0, 0, -1)
	if beforeInclusive.Before(from) {
		return nil, pricefetcher.ErrNoData
	}

	var allBars []client.EODBar
	chunkStart := from
	for chunkStart.Before(beforeInclusive) || chunkStart.Equal(beforeInclusive) {
		chunkEnd := chunkStart.AddDate(0, 0, maxChunkDays-1)
		if chunkEnd.After(beforeInclusive) {
			chunkEnd = beforeInclusive
		}
		fromStr := chunkStart.Format("2006-01-02")
		toStr := chunkEnd.Format("2006-01-02")

		bars, err := c.EODPrices(ctx, symbol, fromStr, toStr)
		p.reportOutcome(ctx, err)
		if err != nil {
			var nf *client.ErrNotFound
			if errors.As(err, &nf) {
				return nil, &pricefetcher.ErrPermanent{Reason: "symbol not found: " + symbol}
			}
			var sl *client.ErrSubscriptionLimit
			if errors.As(err, &sl) {
				return nil, &pricefetcher.ErrPermanent{Reason: sl.Error()}
			}
			return nil, err
		}
		allBars = append(allBars, bars...)
		chunkStart = chunkEnd.AddDate(0, 0, 1)
	}

	if len(allBars) == 0 {
		return nil, pricefetcher.ErrNoData
	}

	result := make([]pricefetcher.DailyBar, len(allBars))
	for i, b := range allBars {
		d, err := time.Parse("2006-01-02", b.Date)
		if err != nil {
			continue
		}
		// The provider decodes JSON floats, so this is the seam where a provider
		// value becomes exact. NewFromFloat takes the shortest representation
		// that round-trips, so 123.45 stays 123.45.
		o := decimal.NewFromFloat(b.Open)
		h := decimal.NewFromFloat(b.High)
		l := decimal.NewFromFloat(b.Low)
		v := b.Volume
		ac := decimal.NewFromFloat(b.AdjClose)
		result[i] = pricefetcher.DailyBar{
			Date:   d,
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  decimal.NewFromFloat(b.Close),
			Volume: &v,
			// EODHD's /api/eod OHLC is as-traded; adjusted_close is its own
			// separate, provider-adjusted series.
			AdjustedClose: &ac,
		}
	}
	if fxExp != 0 {
		result = pricefetcher.ScaleBars(result, fxExp)
	}
	return &pricefetcher.FetchResult{Bars: result}, nil
}

// symbolForAssetClass picks the EODHD API symbol from identifiers.
// For FX pairs it also returns the power of ten to shift a derived pair's rates
// by; otherwise 0.
func (p *Plugin) symbolForAssetClass(ids []pricefetcher.Identifier, assetClass string) (string, int32) {
	if assetClass == db.AssetClassFX {
		for _, id := range ids {
			if id.Type == "FX_PAIR" && id.Value != "" {
				source, exp := pricefetcher.RewriteFXPair(id.Value)
				return source + ".FOREX", exp
			}
		}
		return "", 0
	}
	// Stock/ETF: need {ticker}.{exchange_code}
	// Prefer provider-specific EODHD exchange code over MIC lookup.
	var ticker string
	for _, id := range ids {
		if (id.Type == "MIC_TICKER" || id.Type == "OPENFIGI_TICKER") && id.Value != "" {
			ticker = id.Value
			break
		}
	}
	if ticker != "" {
		for _, id := range ids {
			if id.Type == "EODHD_EXCH_CODE" && id.Value != "" {
				return ticker + "." + id.Value, 0
			}
		}
	}
	// Fallback: resolve MIC domain to EODHD code via exchange map.
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code, 0
			}
		}
	}
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code, 0
			}
		}
	}
	return "", 0
}

func (p *Plugin) micToEODHDCode(mic string) string {
	if p.exchMap == nil || mic == "" {
		return ""
	}
	code, ok := p.exchMap.MICToEODHDCode(mic)
	if !ok {
		return ""
	}
	return code
}

const (
	counterSucceeded = "prices.fetch.eodhd.request.succeeded"
	counterFailed    = "prices.fetch.eodhd.request.failed"
	counterRateLimit = "prices.fetch.eodhd.request.rate_limit"
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
	p.client = client.New(cfg.EODHDAPIKey, cfg.EODHDBaseURL, limiter, p.log, p.httpClient)
	p.lastConfig = raw
	return p.client, nil
}
