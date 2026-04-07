// Package corporateevents implements the Massive corporate event plugin.
//
// The plugin queries two endpoints per held instrument and date range:
//
//	GET /v3/reference/splits?ticker=...&execution_date.gte=...&execution_date.lte=...
//	GET /v3/reference/dividends?ticker=...&ex_dividend_date.gte=...&ex_dividend_date.lte=...
//
// Both endpoints support per-ticker filtering, so a per-ticker loop is the
// natural fit. Pagination is handled by the underlying client via next_url.
package corporateevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and plugin_config.
const PluginID = "massive"

type configJSON struct {
	MassiveAPIKey  string `json:"massive_api_key"`
	MassiveBaseURL string `json:"massive_base_url"`
	CallsPerMin    *int   `json:"massive_calls_per_min"`
}

// Plugin implements corporateevents.Plugin using the Massive corporate
// actions endpoints.
type Plugin struct {
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client

	mu         sync.Mutex
	client     *client.Client
	lastConfig string
}

// NewPlugin returns a plugin. counter and log are optional.
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
	return []string{"MIC_TICKER", "OPENFIGI_TICKER"}
}

func (p *Plugin) AcceptableAssetClasses() map[string]bool {
	return map[string]bool{
		db.AssetClassStock: true,
		db.AssetClassETF:   true,
	}
}

func (p *Plugin) AcceptableExchanges() map[string]bool { return nil }

// AcceptableCurrencies returns nil. Massive's corporate-actions endpoints
// cover US-listed instruments only; the assertion that the dividend currency
// matches the instrument currency is left to the caller / display layer.
func (p *Plugin) AcceptableCurrencies() map[string]bool { return nil }

func (p *Plugin) FetchEvents(ctx context.Context, config []byte, identifiers []corporateevents.Identifier, assetClass string, from, to time.Time) (*corporateevents.Events, error) {
	ticker := tickerFromIdentifiers(identifiers)
	if ticker == "" {
		return nil, corporateevents.ErrNoData
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, err
	}

	fromStr := from.Format("2006-01-02")
	toStr := to.Format("2006-01-02")

	splits, err := c.Splits(ctx, ticker, fromStr, toStr)
	p.reportOutcome(ctx, err)
	if err != nil {
		return nil, p.translateError(err, ticker)
	}
	dividends, err := c.Dividends(ctx, ticker, fromStr, toStr)
	p.reportOutcome(ctx, err)
	if err != nil {
		return nil, p.translateError(err, ticker)
	}

	out := &corporateevents.Events{}
	for _, s := range splits {
		ev, ok := parseSplit(s)
		if !ok {
			if p.log != nil {
				p.log.WarnContext(ctx, "massive corporate event: bad split row",
					"ticker", ticker, "row", s)
			}
			continue
		}
		out.Splits = append(out.Splits, ev)
	}
	for _, d := range dividends {
		ev, ok := parseDividend(d)
		if !ok {
			if p.log != nil {
				p.log.WarnContext(ctx, "massive corporate event: bad dividend row",
					"ticker", ticker, "row", d)
			}
			continue
		}
		out.CashDividends = append(out.CashDividends, ev)
	}
	return out, nil
}

// translateError maps client-layer errors to corporateevents error types.
// 404 -> permanent (ticker not carried). 401/403 -> permanent (auth/plan).
// Rate limit -> transient. Anything else propagates as a transient error.
func (p *Plugin) translateError(err error, ticker string) error {
	var nf *client.ErrNotFound
	if errors.As(err, &nf) {
		return &corporateevents.ErrPermanent{Reason: "ticker not found: " + ticker}
	}
	var fb *client.ErrForbidden
	if errors.As(err, &fb) {
		return &corporateevents.ErrPermanent{Reason: fb.Error()}
	}
	var rl *client.ErrRateLimit
	if errors.As(err, &rl) {
		return &corporateevents.ErrTransient{Reason: rl.Error()}
	}
	return &corporateevents.ErrTransient{Reason: err.Error()}
}

// tickerFromIdentifiers picks a Massive ticker symbol from identifiers.
// Massive uses bare tickers (no exchange suffix) for US securities.
func tickerFromIdentifiers(ids []corporateevents.Identifier) string {
	for _, id := range ids {
		if id.Type == "MIC_TICKER" && id.Value != "" {
			return id.Value
		}
	}
	for _, id := range ids {
		if id.Type == "OPENFIGI_TICKER" && id.Value != "" {
			return id.Value
		}
	}
	return ""
}

func parseSplit(s client.SplitResult) (corporateevents.Split, bool) {
	d, err := time.Parse("2006-01-02", s.ExecutionDate)
	if err != nil || s.SplitFrom <= 0 || s.SplitTo <= 0 {
		return corporateevents.Split{}, false
	}
	return corporateevents.Split{
		ExDate:    d,
		SplitFrom: formatFloat(s.SplitFrom),
		SplitTo:   formatFloat(s.SplitTo),
	}, true
}

func parseDividend(r client.DividendResult) (corporateevents.CashDividend, bool) {
	ex, err := time.Parse("2006-01-02", r.ExDividendDate)
	if err != nil || r.CashAmount < 0 {
		return corporateevents.CashDividend{}, false
	}
	d := corporateevents.CashDividend{
		ExDate:    ex,
		Amount:    formatFloat(r.CashAmount),
		Currency:  r.Currency,
		Frequency: frequencyFromInt(r.Frequency),
	}
	if t, err := time.Parse("2006-01-02", r.PayDate); err == nil {
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

// frequencyFromInt translates Massive's payments-per-year integer into a
// canonical lowercase string. Empty when unknown.
func frequencyFromInt(freq int) string {
	switch freq {
	case 1:
		return "annual"
	case 2:
		return "semi-annual"
	case 4:
		return "quarterly"
	case 12:
		return "monthly"
	default:
		return ""
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

const (
	counterSucceeded = "corporate_events.fetch.massive.request.succeeded"
	counterFailed    = "corporate_events.fetch.massive.request.failed"
	counterRateLimit = "corporate_events.fetch.massive.request.rate_limit"
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
			return nil, fmt.Errorf("massive corporate events: parse config: %w", err)
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
