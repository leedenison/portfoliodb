package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "eodhd"

type configJSON struct {
	EODHDAPIKey  string `json:"eodhd_api_key"`
	EODHDBaseURL string `json:"eodhd_base_url"`
	CallsPerMin  *int   `json:"eodhd_calls_per_min"`
}

// Plugin implements identifier.Plugin using the EODHD REST API.
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

func (p *Plugin) DisplayName() string { return "EODHD" }

func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{}
	out, _ := json.Marshal(cfg)
	return out
}

func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock: true,
	}
}

func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	if len(identifierHints) == 0 {
		return nil, nil, identifier.ErrNotIdentified
	}

	c, err := p.getClient(config)
	if err != nil {
		return nil, nil, err
	}

	query, queryType := pickQuery(identifierHints)
	if query == "" {
		return nil, nil, identifier.ErrNotIdentified
	}

	var opts []client.SearchOption
	if hints.ExchangeCode != "" {
		opts = append(opts, client.WithExchange(hints.ExchangeCode))
	}
	opts = append(opts, client.WithLimit(10))

	results, err := c.Search(ctx, query, opts...)
	if err != nil {
		var nf *client.ErrNotFound
		if errors.As(err, &nf) {
			p.reportOutcome(ctx, identifier.ErrNotIdentified)
			return nil, nil, identifier.ErrNotIdentified
		}
		p.reportOutcome(ctx, err)
		return nil, nil, err
	}

	match := bestMatch(results, hints.ExchangeCode)
	if match == nil {
		p.reportOutcome(ctx, identifier.ErrNotIdentified)
		return nil, nil, identifier.ErrNotIdentified
	}

	// For ISIN lookups where the search matched, verify the ISIN is present
	// on the result (the Search API is fuzzy and may match by name).
	if queryType == "ISIN" && match.ISIN != query {
		p.reportOutcome(ctx, identifier.ErrNotIdentified)
		return nil, nil, identifier.ErrNotIdentified
	}

	inst, ids := stockFromSearch(match)
	if inst == nil {
		p.reportOutcome(ctx, identifier.ErrNotIdentified)
		return nil, nil, identifier.ErrNotIdentified
	}

	p.reportOutcome(ctx, nil)
	return inst, ids, nil
}

const (
	counterSucceeded = "instruments.identification.eodhd.request.succeeded"
	counterFailed    = "instruments.identification.eodhd.request.failed"
	counterRateLimit = "instruments.identification.eodhd.request.rate_limit"
)

func (p *Plugin) reportOutcome(ctx context.Context, err error) {
	if p.counter == nil {
		return
	}
	switch {
	case err == nil:
		p.counter.Incr(ctx, counterSucceeded)
	case errors.Is(err, identifier.ErrNotIdentified):
		// Not a request outcome; don't count.
	default:
		var rl *client.ErrRateLimit
		if errors.As(err, &rl) {
			p.counter.Incr(ctx, counterRateLimit)
		} else {
			p.counter.Incr(ctx, counterFailed)
		}
	}
}

// getClient returns the shared client, rebuilding it only when config changes.
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

// pickQuery selects the best query string and its type from identifier hints.
// Prefers MIC_TICKER/OPENFIGI_TICKER over ISIN.
func pickQuery(hints []identifier.Identifier) (string, string) {
	var isin string
	for _, h := range hints {
		if (h.Type == "MIC_TICKER" || h.Type == "OPENFIGI_TICKER") && h.Value != "" {
			return identifier.NormalizeSplitTicker(h.Value, "-"), h.Type
		}
		if h.Type == "ISIN" && h.Value != "" && isin == "" {
			isin = h.Value
		}
	}
	if isin != "" {
		return isin, "ISIN"
	}
	return "", ""
}
