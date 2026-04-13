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

func (p *Plugin) FetchPrices(ctx context.Context, config []byte, identifiers []pricefetcher.Identifier, assetClass string, from, to time.Time) (*pricefetcher.FetchResult, error) {
	symbol, fxDivisor := p.symbolForAssetClass(identifiers, assetClass)
	if symbol == "" {
		return nil, pricefetcher.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	// to is exclusive in our convention; EODHD API is inclusive.
	toInclusive := to.AddDate(0, 0, -1)
	if toInclusive.Before(from) {
		return nil, pricefetcher.ErrNoData
	}

	var allBars []client.EODBar
	chunkStart := from
	for chunkStart.Before(toInclusive) || chunkStart.Equal(toInclusive) {
		chunkEnd := chunkStart.AddDate(0, 0, maxChunkDays-1)
		if chunkEnd.After(toInclusive) {
			chunkEnd = toInclusive
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
		o := b.Open
		h := b.High
		l := b.Low
		v := b.Volume
		result[i] = pricefetcher.DailyBar{
			Date:   d,
			Open:   &o,
			High:   &h,
			Low:    &l,
			Close:  b.Close,
			Volume: &v,
		}
	}
	if fxDivisor != 1 {
		result = pricefetcher.ScaleBars(result, fxDivisor)
	}
	return &pricefetcher.FetchResult{Bars: result}, nil
}

// symbolForAssetClass picks the EODHD API symbol from identifiers.
// For FX pairs it also returns a divisor for derived pairs; otherwise 1.
func (p *Plugin) symbolForAssetClass(ids []pricefetcher.Identifier, assetClass string) (string, float64) {
	if assetClass == db.AssetClassFX {
		for _, id := range ids {
			if id.Type == "FX_PAIR" && id.Value != "" {
				source, divisor := pricefetcher.RewriteFXPair(id.Value)
				return source + ".FOREX", divisor
			}
		}
		return "", 1
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
				return ticker + "." + id.Value, 1
			}
		}
	}
	// Fallback: resolve MIC domain to EODHD code via exchange map.
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code, 1
			}
		}
	}
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code, 1
			}
		}
	}
	return "", 1
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
	p.client = client.New(cfg.EODHDAPIKey, cfg.EODHDBaseURL, limiter, p.log, p.httpClient)
	p.lastConfig = raw
	return p.client, nil
}
