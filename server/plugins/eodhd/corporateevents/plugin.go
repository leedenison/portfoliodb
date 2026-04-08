// Package corporateevents implements the EODHD corporate event plugin.
//
// The plugin queries two endpoints per held instrument and date range:
//
//	GET /api/splits/{TICKER}.{EXCHANGE}?from=YYYY-MM-DD&to=YYYY-MM-DD&fmt=json
//	GET /api/div/{TICKER}.{EXCHANGE}?from=YYYY-MM-DD&to=YYYY-MM-DD&fmt=json
//
// EODHD's bulk corporate-action endpoint cannot be filtered by symbol for
// splits/dividends, only by exchange and date, so a per-ticker loop is used.
// Both responses are simple JSON arrays; no pagination is required.
package corporateevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and plugin_config.
const PluginID = "eodhd"

type configJSON struct {
	EODHDAPIKey  string `json:"eodhd_api_key"`
	EODHDBaseURL string `json:"eodhd_base_url"`
	CallsPerMin  *int   `json:"eodhd_calls_per_min"`
}

// Plugin implements corporateevents.Plugin using the EODHD splits and
// dividends APIs.
type Plugin struct {
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap

	mu         sync.Mutex
	client     *client.Client
	lastConfig string
}

// NewPlugin returns a plugin. counter, log and exchMap are optional.
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
	return []string{"MIC_TICKER", "OPENFIGI_TICKER"}
}

func (p *Plugin) AcceptableAssetClasses() map[string]bool {
	return map[string]bool{
		db.AssetClassStock: true,
		db.AssetClassETF:   true,
	}
}

func (p *Plugin) AcceptableExchanges() map[string]bool { return nil }

func (p *Plugin) AcceptableCurrencies() map[string]bool { return nil }

func (p *Plugin) FetchEvents(ctx context.Context, config []byte, identifiers []corporateevents.Identifier, assetClass string, from, to time.Time) (*corporateevents.Events, error) {
	symbol := p.symbolFromIdentifiers(identifiers)
	if symbol == "" {
		return nil, corporateevents.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	fromStr := from.Format("2006-01-02")
	toStr := to.Format("2006-01-02")

	splits, err := c.Splits(ctx, symbol, fromStr, toStr)
	p.reportOutcome(ctx, err)
	if err != nil {
		return nil, p.translateError(err, symbol)
	}
	dividends, err := c.Dividends(ctx, symbol, fromStr, toStr)
	p.reportOutcome(ctx, err)
	if err != nil {
		return nil, p.translateError(err, symbol)
	}

	out := &corporateevents.Events{}
	for _, s := range splits {
		ev, ok := parseSplit(s)
		if !ok {
			if p.log != nil {
				p.log.WarnContext(ctx, "eodhd corporate event: bad split row",
					"symbol", symbol, "row", s)
			}
			continue
		}
		out.Splits = append(out.Splits, ev)
	}
	for _, d := range dividends {
		ev, ok := parseDividend(d)
		if !ok {
			if p.log != nil {
				p.log.WarnContext(ctx, "eodhd corporate event: bad dividend row",
					"symbol", symbol, "row", d)
			}
			continue
		}
		out.CashDividends = append(out.CashDividends, ev)
	}
	return out, nil
}

// translateError maps client-layer errors to corporateevents error types.
// 404 -> permanent (instrument not carried). Subscription limit -> permanent.
// Rate limit -> transient. Anything else propagates as a transient generic
// error so the worker leaves the gap untouched and retries on the next cycle.
func (p *Plugin) translateError(err error, symbol string) error {
	var nf *client.ErrNotFound
	if errors.As(err, &nf) {
		return &corporateevents.ErrPermanent{Reason: "symbol not found: " + symbol}
	}
	var sl *client.ErrSubscriptionLimit
	if errors.As(err, &sl) {
		return &corporateevents.ErrPermanent{Reason: sl.Error()}
	}
	var rl *client.ErrRateLimit
	if errors.As(err, &rl) {
		return &corporateevents.ErrTransient{Reason: rl.Error()}
	}
	return &corporateevents.ErrTransient{Reason: err.Error()}
}

// symbolFromIdentifiers picks the EODHD API symbol from identifiers.
// Format: {ticker}.{eodhd_exchange_code}.
func (p *Plugin) symbolFromIdentifiers(ids []corporateevents.Identifier) string {
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code
			}
		}
	}
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" && id.Value != "" {
			if code := p.micToEODHDCode(id.Domain); code != "" {
				return id.Value + "." + code
			}
		}
	}
	return ""
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

// parseSplit converts an EODHD split row into a corporate event split. The
// EODHD "split" field is "{to}/{from}" (both decimal strings), so a 4:1
// forward split is "4.000000/1.000000". The numerator and denominator are
// normalized to canonical decimal form ("4.000000" -> "4") via parseFloat +
// formatFloat so the stored values are stable across providers.
func parseSplit(s client.SplitRow) (corporateevents.Split, bool) {
	d, err := time.Parse("2006-01-02", s.Date)
	if err != nil {
		return corporateevents.Split{}, false
	}
	parts := strings.SplitN(s.Split, "/", 2)
	if len(parts) != 2 {
		return corporateevents.Split{}, false
	}
	to, ok := normalizeDecimal(strings.TrimSpace(parts[0]))
	if !ok {
		return corporateevents.Split{}, false
	}
	from, ok := normalizeDecimal(strings.TrimSpace(parts[1]))
	if !ok {
		return corporateevents.Split{}, false
	}
	return corporateevents.Split{
		ExDate:    d,
		SplitFrom: from,
		SplitTo:   to,
	}, true
}

// normalizeDecimal parses a positive decimal string and re-formats it via
// strconv.FormatFloat with -1 precision so trailing zeros are removed.
// Returns ("", false) when the input does not parse or is non-positive.
func normalizeDecimal(s string) (string, bool) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return "", false
	}
	return strconv.FormatFloat(v, 'f', -1, 64), true
}

// parseDividend converts an EODHD dividend row into a corporate event cash
// dividend. UnadjustedValue is preferred over Value because Value is
// retroactively adjusted by EODHD when later splits occur, which would cause
// the stored amount to drift over time.
func parseDividend(r client.DividendRow) (corporateevents.CashDividend, bool) {
	ex, err := time.Parse("2006-01-02", r.Date)
	if err != nil {
		return corporateevents.CashDividend{}, false
	}
	amount := r.UnadjustedValue
	if amount == 0 {
		amount = r.Value
	}
	if amount < 0 {
		return corporateevents.CashDividend{}, false
	}
	d := corporateevents.CashDividend{
		ExDate:    ex,
		Amount:    formatFloat(amount),
		Currency:  r.Currency,
		Frequency: normalizeFrequency(r.Period),
	}
	if t, err := time.Parse("2006-01-02", r.PaymentDate); err == nil {
		d.PayDate = t
	}
	if t, err := time.Parse("2006-01-02", r.RecordDate); err == nil {
		d.RecordDate = t
	}
	if t, err := time.Parse("2006-01-02", r.DeclarationDate); err == nil {
		d.DeclarationDate = t
	}
	return d, true
}

// normalizeFrequency lowercases EODHD's period strings (e.g. "Quarterly" ->
// "quarterly") to a single canonical form. Empty strings pass through.
func normalizeFrequency(period string) string {
	return strings.ToLower(strings.TrimSpace(period))
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

const (
	counterSucceeded = "corporate_events.fetch.eodhd.request.succeeded"
	counterFailed    = "corporate_events.fetch.eodhd.request.failed"
	counterRateLimit = "corporate_events.fetch.eodhd.request.rate_limit"
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
			return nil, fmt.Errorf("eodhd corporate events: parse config: %w", err)
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
