package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "eodhd"

// Plugin implements identifier.Plugin using the EODHD REST API.
type Plugin struct {
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap

	cache client.Cache
}

// NewPlugin returns a plugin. log and exchMap are optional (nil for tests).
func NewPlugin(log *slog.Logger, httpClient *http.Client, exchMap *exchangemap.ExchangeMap) *Plugin {
	return &Plugin{log: log, httpClient: httpClient, exchMap: exchMap}
}

func (p *Plugin) DisplayName() string { return "EODHD" }

func (p *Plugin) DefaultConfig() []byte {
	cfg := client.Config{}
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

	// Whatever was asked is verified against the result. The Search API is fuzzy
	// and matches on the name as readily as on the code, so a search for one
	// symbol answers with another company's listing and bestMatch takes it. That
	// result is a whole answer -- a name, a currency, a venue and an ISIN -- and
	// merge admission compares it against the winner's, so an unverified one
	// reaches the security a resolution landed on.
	//
	// The ticker is compared on one spelling: pickQuery renders the query with
	// EODHD's hyphen and a provider writes a class separator however it likes,
	// so both sides are normalized before they meet.
	switch queryType {
	case "ISIN":
		if match.ISIN != query {
			return result(nil, nil, identifier.ErrNotIdentified)
		}
	default:
		if !strings.EqualFold(identifier.NormalizeSplitTicker(match.Code, "-"), query) {
			return result(nil, nil, identifier.ErrNotIdentified)
		}
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
	c, _, err := p.cache.Get(config, p.log, p.httpClient)
	return c, err
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
