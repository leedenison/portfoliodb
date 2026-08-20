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
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
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
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap

	mu         sync.Mutex
	client     *client.Client
	lastConfig string
}

// NewPlugin returns a plugin. log and exchMap are optional (nil for tests).
func NewPlugin(log *slog.Logger, httpClient *http.Client, exchMap *exchangemap.ExchangeMap) *Plugin {
	return &Plugin{log: log, httpClient: httpClient, exchMap: exchMap}
}

func (p *Plugin) DisplayName() string { return "EODHD" }

func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{}
	out, _ := json.Marshal(cfg)
	return out
}

func (p *Plugin) AcceptableInstrumentKinds() map[string]bool {
	return map[string]bool{identifier.InstrumentKindSecurity: true}
}

func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock: true,
	}
}

func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident identifier.Identity) (identifier.Result, error) {
	identifierHints := ident.Stated
	if len(identifierHints) == 0 {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	c, err := p.getClient(config)
	if err != nil {
		return result(nil, nil, err)
	}

	query, queryType := pickQuery(identifierHints)
	if query == "" {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	exchHint := exchangeHintFromIdentifiers(identifierHints)
	var opts []client.SearchOption
	if exchHint != "" {
		opts = append(opts, client.WithExchange(exchHint))
	}
	opts = append(opts, client.WithLimit(10))

	results, err := c.Search(ctx, query, opts...)
	if err != nil {
		var nf *client.ErrNotFound
		if errors.As(err, &nf) {
			return result(nil, nil, identifier.ErrNotIdentified)
		}
		return result(nil, nil, err)
	}

	match := bestMatch(results, exchHint)
	if match == nil {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	// For ISIN lookups where the search matched, verify the ISIN is present
	// on the result (the Search API is fuzzy and may match by name).
	if queryType == "ISIN" && match.ISIN != query {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	inst, ids := stockFromSearch(match, p.exchMap)
	if inst == nil {
		return result(nil, nil, identifier.ErrNotIdentified)
	}

	return result(inst, ids, nil)
}

// result pairs the identification with the outcome the resolver records for
// this call. An ErrNotIdentified from before the request was made is reported
// the same as one from an empty response: neither identified the instrument,
// and the plugin has nothing further to distinguish them by.
func result(inst *identifier.Instrument, ids []identifier.Identifier, err error) (identifier.Result, error) {
	return identifier.Result{
		Instrument:  inst,
		Identifiers: ids,
		Telemetry:   identifier.Telemetry{Outcome: outcome(err)},
	}, err
}

// outcome classifies an Identify error into the vocabulary the resolver records.
func outcome(err error) identifier.Outcome {
	switch {
	case err == nil:
		return identifier.OutcomeIdentified
	case errors.Is(err, identifier.ErrNotIdentified):
		return identifier.OutcomeNotIdentified
	case errors.Is(err, context.DeadlineExceeded):
		return identifier.OutcomeTimeout
	default:
		var rl *client.ErrRateLimit
		if errors.As(err, &rl) {
			return identifier.OutcomeRateLimited
		}
		return identifier.OutcomeError
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

// exchangeHintFromIdentifiers returns the Domain of the first OPENFIGI_TICKER
// hint, which uses Bloomberg exchange codes compatible with EODHD's search API.
func exchangeHintFromIdentifiers(hints []identifier.Identifier) string {
	for _, h := range hints {
		if h.Type == "OPENFIGI_TICKER" && h.Domain != "" {
			return h.Domain
		}
	}
	return ""
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
