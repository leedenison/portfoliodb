package identifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
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
	openfigi   *OpenFIGIClient
	config     configJSON
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap
}

// NewPlugin returns a plugin. Counter and log are optional (nil for tests); when set, OpenFIGI calls are counted and logged.
// exchMap may be nil (exchange resolution is best-effort).
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger, httpClient *http.Client, exchMap *exchangemap.ExchangeMap) *Plugin {
	return &Plugin{counter: counter, log: log, httpClient: httpClient, exchMap: exchMap}
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

// AcceptableInstrumentKinds returns only Security.
func (p *Plugin) AcceptableInstrumentKinds() map[string]bool {
	return map[string]bool{identifier.InstrumentKindSecurity: true}
}

// AcceptableSecurityTypes returns the security type hints this plugin can attempt identification for.
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock:       true,
		identifier.SecurityTypeHintETF:         true,
		identifier.SecurityTypeHintFixedIncome: true,
		identifier.SecurityTypeHintMutualFund:  true,
		identifier.SecurityTypeHintOption:      true,
		identifier.SecurityTypeHintFuture:      true,
		identifier.SecurityTypeHintFX:          true,
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
	p.openfigi = NewOpenFIGIClient(cfg.OpenFIGIAPIKey, baseURL, p.counter, p.log, p.httpClient)

	if len(identifierHints) == 0 {
		return nil, nil, identifier.ErrNotIdentified
	}
	// Use OpenFIGI Mapping only (no Search API); try first hint that we can map
	results, matchedHint, err := p.tryOpenFIGIFromHints(ctx, identifierHints, hints)
	if err != nil {
		return nil, nil, err
	}
	if inst, ids, ok := p.resolveResults(results, hints, true); ok {
		// When the matched hint was a MIC_TICKER, include it in the returned
		// identifiers. A successful Mapping API response for that ticker proves
		// the association. Other hint types (ISIN, CUSIP, etc.) are not appended
		// because OpenFIGI may return corrected values for those.
		if matchedHint != nil && matchedHint.Type == "MIC_TICKER" {
			hasMICTicker := false
			for _, id := range ids {
				if id.Type == "MIC_TICKER" && id.Value == matchedHint.Value {
					hasMICTicker = true
					break
				}
			}
			if !hasMICTicker {
				ids = append(ids, *matchedHint)
			}
		}
		return inst, ids, nil
	}
	return nil, nil, identifier.ErrNotIdentified
}

// resolveResults picks a result from the slice and converts it to an instrument.
// For derivatives, UnderlyingIdentifiers are populated so the resolution layer can
// resolve the underlying through the full plugin pipeline.
// When multiple results exist, the SecurityTypeHint is used to prefer results
// whose classified asset class matches the hint. The stored asset class is always
// derived from the selected result's OpenFIGI fields via classify, never from the hint.
// If fallbackFirst is true and no hint match is found, the first result is used.
// It returns (inst, ids, true) when a result was chosen, (nil, nil, false) otherwise.
func (p *Plugin) resolveResults(results []OpenFIGIResult, hints identifier.Hints, fallbackFirst bool) (*identifier.Instrument, []identifier.Identifier, bool) {
	if len(results) == 0 {
		return nil, nil, false
	}
	idx := 0
	if len(results) > 1 {
		idx = -1
		if hints.SecurityTypeHint != "" {
			for i := range results {
				ac := classify(results[i].SecurityType, results[i].SecurityType2, results[i].MarketSector)
				if ac == hints.SecurityTypeHint {
					idx = i
					break
				}
			}
		}
		if idx < 0 && fallbackFirst {
			idx = 0
		} else if idx < 0 {
			return nil, nil, false
		}
	}
	inst, ids := openFIGIResultToInstrument(&results[idx], p.exchMap)
	if isDerivative(&results[idx]) {
		parsed, ok := derivative.ParseOptionTicker(results[idx].Ticker)
		if !ok || parsed.Symbol == "" {
			return nil, nil, false
		}
		inst.UnderlyingIdentifiers = []identifier.Identifier{
			{Type: "MIC_TICKER", Value: parsed.Symbol},
		}
		// Convert parsed option ticker to OCC and replace OPENFIGI_TICKER.
		if occ, ok := derivative.BuildOCCCompact(parsed.Symbol, parsed.Expiry, parsed.PutCall, parsed.Strike); ok {
			replaced := ids[:0]
			for _, id := range ids {
				if id.Type != "OPENFIGI_TICKER" {
					replaced = append(replaced, id)
				}
			}
			ids = append(replaced, identifier.Identifier{Type: "OCC", Value: occ})
		}
	}
	return inst, ids, true
}

// openFIGIIDTypeFromHint maps our identifier type (proto IdentifierType name) to OpenFIGI Mapping API idType.
// Returns empty string if the hint type is not supported by OpenFIGI Mapping.
var openFIGIIDTypeFromHint = map[string]string{
	"MIC_TICKER": "TICKER", "OPENFIGI_TICKER": "TICKER", "ISIN": "ID_ISIN", "CUSIP": "ID_CUSIP", "SEDOL": "ID_SEDOL", "CINS": "ID_CINS", "WERTPAPIER": "ID_WERTPAPIER",
	"OCC": "OCC_SYMBOL", "OPRA": "OPRA_SYMBOL", "FUT_OPT": "UNIQUE_ID_FUT_OPT",
	"OPENFIGI_SHARE_CLASS": "ID_BB_GLOBAL_SHARE_CLASS_LEVEL", "OPENFIGI_COMPOSITE": "COMPOSITE_ID_BB_GLOBAL",
}

// tryOpenFIGIFromHints tries OpenFIGI Mapping for each hint (in order); returns the first non-empty result set
// and the hint that produced it. Uses only Mapping API (no Search). For MIC_TICKER hints, Domain is sent as
// micCode; for OPENFIGI_TICKER, as exchCode.
// We do not use the security type hint as securityType2 (our vocabulary does not match OpenFIGI's). The plugin already prefers EQUITY+common when multiple results exist.
func (p *Plugin) tryOpenFIGIFromHints(ctx context.Context, identifierHints []identifier.Identifier, hints identifier.Hints) ([]OpenFIGIResult, *identifier.Identifier, error) {
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
		if idType == "TICKER" {
			idValue = identifier.NormalizeSplitTicker(value, "/")
		}
		if idType == "OCC_SYMBOL" {
			padded, ok := derivative.OCCPadded(value)
			if !ok {
				continue
			}
			idValue = padded
		}
		job := MappingJob{IDType: idType, IDValue: idValue}
		// MIC_TICKER Domain carries an ISO 10383 MIC (e.g. "XNAS") set by
		// other plugins (Massive, EODHD). We intentionally do NOT pass it as
		// micCode to OpenFIGI because OpenFIGI matches MICs precisely: e.g.
		// NASDAQ has several MICs (XNAS, XNGS, XNMS) and a ticker listed on
		// XNGS will not match a query filtered to XNAS. Since callers may map
		// an exchange to the wrong MIC, it is safer to omit the filter.
		if ourType == "OPENFIGI_TICKER" && h.Domain != "" {
			job.ExchCode = h.Domain
		}
		if hints.Currency != "" {
			job.Currency = hints.Currency
		}
		results, err := p.openfigi.Mapping(ctx, job)
		if err != nil {
			return nil, nil, err
		}
		if len(results) > 0 {
			return results, &h, nil
		}
	}
	return nil, nil, nil
}

