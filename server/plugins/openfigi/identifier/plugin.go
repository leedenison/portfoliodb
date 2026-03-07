package identifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "openfigi"

// configJSON is the shape of the plugin's config from identifier_plugin_config.config.
type configJSON struct {
	OpenFIGIAPIKey  string `json:"openfigi_api_key"`
	OpenFIGIBaseURL string `json:"openfigi_base_url"` // for testing
}

// Plugin implements identifier.Plugin using OpenFIGI Mapping only (no Search, no OpenAI).
type Plugin struct {
	openfigi *OpenFIGIClient
	config   configJSON
	counter  telemetry.CounterIncrementer
	log      *slog.Logger
}

// NewPlugin returns a plugin. Counter and log are optional (nil for tests); when set, OpenFIGI calls are counted and logged.
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger) *Plugin {
	return &Plugin{counter: counter, log: log}
}

// DisplayName returns a human-readable name for the plugin.
func (p *Plugin) DisplayName() string {
	return "OpenFIGI"
}

// DefaultConfig returns default config JSON with the keys the plugin uses and empty/dummy values for the user to fill in via Admin UI.
func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{
		OpenFIGIAPIKey:  "",
		OpenFIGIBaseURL: "",
	}
	out, _ := json.Marshal(cfg)
	return out
}

// Identify resolves using identifier hints (mapping) or returns ErrNotIdentified. Does not use Search API or OpenAI.
// When identifierHints is empty, returns ErrNotIdentified. When non-empty, uses OpenFIGI Mapping only.
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, nil, err
		}
	}
	p.config = cfg
	baseURL := openFIGIBaseURL
	if cfg.OpenFIGIBaseURL != "" {
		baseURL = cfg.OpenFIGIBaseURL
	}
	p.openfigi = NewOpenFIGIClientWithTelemetry(cfg.OpenFIGIAPIKey, baseURL, p.counter, p.log)

	if len(identifierHints) == 0 {
		return nil, nil, identifier.ErrNotIdentified
	}
	// Use OpenFIGI Mapping only (no Search API); try first hint that we can map
	results, err := p.tryOpenFIGIFromHints(ctx, identifierHints, exchangeCodeHint, currencyHint, micHint)
	if err != nil {
		return nil, nil, err
	}
	if inst, ids, ok := p.resolveResults(ctx, results, true); ok {
		return inst, ids, nil
	}
	return nil, nil, identifier.ErrNotIdentified
}

// resolveResults picks a result from the slice, converts it to an instrument, and ensures underlying.
// If fallbackFirst is true and there are multiple results with no EQUITY+common match, the first result is used.
// It returns (inst, ids, true) when a result was chosen, (nil, nil, false) otherwise.
func (p *Plugin) resolveResults(ctx context.Context, results []OpenFIGIResult, fallbackFirst bool) (*identifier.Instrument, []identifier.Identifier, bool) {
	if len(results) == 0 {
		return nil, nil, false
	}
	idx := -1
	if len(results) == 1 {
		idx = 0
	} else {
		for i := range results {
			ac := assetClassFromOpenFIGI(results[i].SecurityType, results[i].SecurityType2, results[i].MarketSector)
			if ac == "EQUITY" && strings.Contains(strings.ToLower(results[i].SecurityType2), "common") {
				idx = i
				break
			}
		}
		if idx < 0 && fallbackFirst {
			idx = 0
		} else if idx < 0 {
			return nil, nil, false
		}
	}
	inst, ids := openFIGIResultToInstrument(&results[idx])
	EnsureUnderlying(ctx, p.openfigi, inst, &results[idx], p.getUnderlyingSymbol)
	return inst, ids, true
}

// openFIGIMappingIDTypes are identifier types that OpenFIGI Mapping API accepts.
var openFIGIMappingIDTypes = map[string]bool{
	"TICKER": true, "FIGI": true, "ISIN": true, "CUSIP": true, "SEDOL": true,
	"COMPOSITE_FIGI": true, "SHARE_CLASS_FIGI": true, "WKN": true, "QUOTE_LISTED": true,
}

// tryOpenFIGIFromHints tries OpenFIGI Mapping for each hint (in order); returns first non-empty result set.
// Uses only Mapping API (no Search). exchangeCodeHint, currencyHint, and micHint narrow results when the hint has no domain.
func (p *Plugin) tryOpenFIGIFromHints(ctx context.Context, hints []identifier.Identifier, exchangeCodeHint, currencyHint, micHint string) ([]OpenFIGIResult, error) {
	for _, h := range hints {
		idType := strings.ToUpper(strings.TrimSpace(h.Type))
		if idType == "" || !openFIGIMappingIDTypes[idType] {
			continue
		}
		value := strings.TrimSpace(h.Value)
		if value == "" {
			continue
		}
		job := MappingJob{IDType: idType, IDValue: value}
		if h.Domain != "" {
			job.ExchCode = h.Domain
		} else if exchangeCodeHint != "" {
			job.ExchCode = exchangeCodeHint
		}
		if currencyHint != "" {
			job.Currency = currencyHint
		}
		if micHint != "" {
			job.MICCode = micHint
		}
		results, err := p.openfigi.Mapping(ctx, job)
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return nil, nil
}

// getUnderlyingSymbol resolves the underlying ticker for a derivative using the derivative library only (no OpenAI).
func (p *Plugin) getUnderlyingSymbol(ctx context.Context, derivativeTicker string) (symbol, exchangeHint string, ok bool) {
	if u, parseOk := derivative.ParseOptionTicker(derivativeTicker); parseOk {
		return u.Symbol, u.ExchangeHint, true
	}
	return "", "", false
}
