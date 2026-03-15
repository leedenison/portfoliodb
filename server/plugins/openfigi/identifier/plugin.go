package identifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
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

// AcceptableSecurityTypes returns the security type hints this plugin can attempt identification for (STOCK, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE; not CASH or UNKNOWN).
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock:       true,
		identifier.SecurityTypeHintFixedIncome: true,
		identifier.SecurityTypeHintMutualFund:  true,
		identifier.SecurityTypeHintOption:      true,
		identifier.SecurityTypeHintFuture:      true,
	}
}

// Identify resolves using identifier hints (mapping) or returns ErrNotIdentified. Does not use Search API or OpenAI.
// When identifierHints is empty, returns ErrNotIdentified. When non-empty, uses OpenFIGI Mapping only.
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
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
	p.openfigi = NewOpenFIGIClient(cfg.OpenFIGIAPIKey, baseURL, p.counter, p.log)

	if len(identifierHints) == 0 {
		return nil, nil, identifier.ErrNotIdentified
	}
	// Use OpenFIGI Mapping only (no Search API); try first hint that we can map
	results, err := p.tryOpenFIGIFromHints(ctx, identifierHints, hints)
	if err != nil {
		return nil, nil, err
	}
	if inst, ids, ok := p.resolveResults(ctx, results, true); ok {
		return inst, ids, nil
	}
	return nil, nil, identifier.ErrNotIdentified
}

// resolveResults picks a result from the slice, converts it to an instrument, and ensures underlying.
// If fallbackFirst is true and there are multiple results with no STOCK+common match, the first result is used.
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
			ac := assetClassFromOpenFIGI(results[i].SecurityType, results[i].SecurityType2, results[i].MarketSector, p.log)
			if ac == db.AssetClassStock && strings.Contains(strings.ToLower(results[i].SecurityType2), "common") {
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
	inst, ids := openFIGIResultToInstrument(&results[idx], p.log)
	if err := EnsureUnderlying(ctx, p.openfigi, inst, &results[idx], p.getUnderlyingSymbol); err != nil {
		return nil, nil, false
	}
	return inst, ids, true
}

// openFIGIIDTypeFromHint maps our identifier type (proto IdentifierType name) to OpenFIGI Mapping API idType.
// Returns empty string if the hint type is not supported by OpenFIGI Mapping.
var openFIGIIDTypeFromHint = map[string]string{
	"TICKER": "TICKER", "ISIN": "ID_ISIN", "CUSIP": "ID_CUSIP", "SEDOL": "ID_SEDOL", "CINS": "ID_CINS", "WERTPAPIER": "ID_WERTPAPIER",
	"OCC": "OCC_SYMBOL", "OPRA": "OPRA_SYMBOL", "FUT_OPT": "UNIQUE_ID_FUT_OPT",
	"OPENFIGI_GLOBAL": "ID_BB_GLOBAL", "OPENFIGI_SHARE_CLASS": "ID_BB_GLOBAL_SHARE_CLASS_LEVEL", "OPENFIGI_COMPOSITE": "COMPOSITE_ID_BB_GLOBAL",
}

// tryOpenFIGIFromHints tries OpenFIGI Mapping for each hint (in order); returns first non-empty result set.
// Uses only Mapping API (no Search). hints.ExchangeCode, Currency (trading currency), MIC narrow results when the hint has no domain.
// We do not use the security type hint as securityType2 (our vocabulary does not match OpenFIGI's). The plugin already prefers EQUITY+common when multiple results exist.
func (p *Plugin) tryOpenFIGIFromHints(ctx context.Context, identifierHints []identifier.Identifier, hints identifier.Hints) ([]OpenFIGIResult, error) {
	for _, h := range identifierHints {
		ourType := strings.TrimSpace(h.Type)
		idType := openFIGIIDTypeFromHint[ourType]
		if idType == "" || ourType == "" {
			continue
		}
		value := strings.TrimSpace(h.Value)
		if value == "" {
			continue
		}
		idValue := value
		if idType == "TICKER" && strings.Contains(value, ".") {
			idValue = strings.ReplaceAll(value, ".", "/")
		}
		job := MappingJob{IDType: idType, IDValue: idValue}
		if h.Domain != "" {
			job.ExchCode = h.Domain
		} else if hints.ExchangeCode != "" {
			job.ExchCode = hints.ExchangeCode
		}
		if hints.Currency != "" {
			job.Currency = hints.Currency
		}
		if hints.MIC != "" {
			job.MICCode = hints.MIC
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
