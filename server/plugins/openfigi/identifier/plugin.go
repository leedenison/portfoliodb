package identifier

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/derivative"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
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
	log        *slog.Logger
	httpClient *http.Client
	exchMap    *exchangemap.ExchangeMap
}

// NewPlugin returns a plugin. log is optional (nil for tests); when set, OpenFIGI calls are logged.
// exchMap may be nil (exchange resolution is best-effort).
func NewPlugin(log *slog.Logger, httpClient *http.Client, exchMap *exchangemap.ExchangeMap) *Plugin {
	return &Plugin{log: log, httpClient: httpClient, exchMap: exchMap}
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
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (identifier.Result, error) {
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return result(nil, nil, err)
		}
	}
	p.config = cfg
	baseURL := openFIGIBaseURL
	if cfg.OpenFIGIBaseURL != "" {
		baseURL = cfg.OpenFIGIBaseURL
	}
	p.openfigi = NewOpenFIGIClient(cfg.OpenFIGIAPIKey, baseURL, p.log, p.httpClient)

	if len(identifierHints) == 0 {
		return result(nil, nil, identifier.ErrNotIdentified)
	}
	// Use OpenFIGI Mapping only (no Search API); try first hint that we can map
	results, matchedHint, err := p.tryOpenFIGIFromHints(ctx, identifierHints, hints)
	if err != nil {
		return result(nil, nil, err)
	}
	if inst, ids, ok := p.resolveResults(results, hints, identifierHints, true); ok {
		if hints.Currency != "" {
			inst.Currency = hints.Currency
		}
		// When the matched hint was a MIC_TICKER, include it in the returned
		// identifiers. A successful Mapping API response for that ticker proves
		// the association. Other hint types (ISIN, CUSIP, etc.) are not appended
		// because OpenFIGI may return corrected values for those.
		//
		// The mapping proves the ticker, not the venue: a bare ticker query
		// returns every listing of that symbol worldwide, so the hint's exchange
		// is only asserted once the chosen result is known to be on it. Asserting
		// it regardless is how a ticker hint for one company came to be stored
		// against a same-ticker listing of a different one.
		if matchedHint != nil && matchedHint.Type == "MIC_TICKER" && p.assertsExchange(inst, matchedHint.Domain) {
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
		return result(inst, ids, nil)
	}
	return result(nil, nil, identifier.ErrNotIdentified)
}

// result pairs the identification with the outcome the resolver records for
// this call. Identify makes at most one mapping call that decides the answer,
// so one call is one outcome.
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
		var rl *ErrRateLimit
		if errors.As(err, &rl) {
			return identifier.OutcomeRateLimited
		}
		return identifier.OutcomeError
	}
}

// exchangeHintMIC returns the ISO 10383 MIC named by the first MIC_TICKER hint
// that carries one, or "" when no hint names an exchange.
func exchangeHintMIC(identifierHints []identifier.Identifier) string {
	for _, h := range identifierHints {
		if h.Type == "MIC_TICKER" {
			if d := strings.TrimSpace(h.Domain); d != "" {
				return d
			}
		}
	}
	return ""
}

// onExchange reports whether a result is listed on the given MIC.
//
// The result's OpenFIGI exchange code is expanded to the MICs it covers, rather
// than the MIC being collapsed to a code: several codes can reach one MIC (XSTO
// is SF, SS and XO), so a MIC has no single code to collapse to. Returns false
// when the answer is unknown -- no exchange map, or a code the map does not
// carry -- which is what keeps an unrankable result from outranking a real
// match.
func (p *Plugin) onExchange(r *OpenFIGIResult, mic string) bool {
	if p.exchMap == nil || mic == "" || r.ExchCode == "" {
		return false
	}
	for _, m := range p.exchMap.ExchCodeToMICs(r.ExchCode) {
		if strings.EqualFold(m, mic) {
			return true
		}
	}
	return false
}

// spansExchange reports whether a result is a market-level listing whose
// country contains the given MIC.
//
// OpenFIGI answers a mapping call with rows from both of its code namespaces,
// and the composite row is the one a US listing usually leads with. Read as a
// venue code it matches nothing, so before this every American result scored
// zero on exchange and ranking fell back to security type alone -- which for a
// ticker listed in a dozen countries is no constraint at all.
//
// It is a weaker claim than onExchange and scores less: the composite says the
// listing is somewhere in that country, not that it is on the named venue.
func (p *Plugin) spansExchange(r *OpenFIGIResult, mic string) bool {
	if p.exchMap == nil || mic == "" || r.ExchCode == "" {
		return false
	}
	country := p.exchMap.CompositeCountry(r.ExchCode)
	return country != "" && strings.EqualFold(country, p.exchMap.MICCountry(mic))
}

// isComposite reports whether a result is the composite listing rather than one
// of the venues under it. OpenFIGI says so structurally by giving the composite
// row its own FIGI as compositeFIGI, which needs no exchange map to read.
func isComposite(r *OpenFIGIResult) bool {
	return r.CompositeFIGI != nil && *r.CompositeFIGI == r.FIGI && r.FIGI != ""
}

// assertsExchange reports whether a hint MIC may be stored against the chosen
// instrument. A hint that names no exchange asserts nothing to check, so it
// passes; otherwise the venue the result named decides.
//
// A market-level result is not the free pass it used to be. Before the composite
// was readable its country came back empty, so every US result reached here with
// nothing to check against and the hint's venue was asserted whatever it said --
// which is how a ticker hint for one company came to be stored against a
// same-ticker listing of another.
func (p *Plugin) assertsExchange(inst *identifier.Instrument, mic string) bool {
	if mic == "" || inst == nil {
		return true
	}
	return inst.Venue.Permits(mic, func(m string) string {
		if p.exchMap == nil {
			return ""
		}
		return p.exchMap.MICCountry(m)
	})
}

// resolveResults picks a result from the slice and converts it to an instrument.
// For derivatives, UnderlyingIdentifiers are populated so the resolution layer can
// resolve the underlying through the full plugin pipeline.
//
// When multiple results exist they are ranked by how much of what the caller
// already said they account for. Naming the venue outright counts for most;
// being a composite listing whose group covers that venue counts for less,
// since it narrows the answer to a market rather than to an exchange; and the
// SecurityTypeHint counts for least, because a ticker is unique within a venue
// and a security type is not. The tiers are ordered so that no combination of
// weaker signals outranks a venue match.
//
// A bare ticker maps to every listing of that symbol worldwide, so without the
// venue the choice among same-class results is arbitrary -- which is how a query
// for a UK stock settled on a same-ticker listing in another market. Ties keep
// the earliest result; when nothing scores at all, fallbackFirst decides.
//
// The stored asset class is always derived from the selected result's OpenFIGI
// fields via classify, never from the hint.
// If fallbackFirst is true and no hint match is found, the first result is used.
// It returns (inst, ids, true) when a result was chosen, (nil, nil, false) otherwise.
func (p *Plugin) resolveResults(results []OpenFIGIResult, hints identifier.Hints, identifierHints []identifier.Identifier, fallbackFirst bool) (*identifier.Instrument, []identifier.Identifier, bool) {
	if len(results) == 0 {
		return nil, nil, false
	}
	idx := 0
	if len(results) > 1 {
		mic := exchangeHintMIC(identifierHints)
		idx = -1
		best := 0
		for i := range results {
			score := 0
			switch {
			case p.onExchange(&results[i], mic):
				score += 4
			case p.spansExchange(&results[i], mic):
				score += 2
			}
			if hints.SecurityTypeHint != "" &&
				classify(results[i].SecurityType, results[i].SecurityType2, results[i].MarketSector) == hints.SecurityTypeHint {
				score++
			}
			if score > best {
				idx, best = i, score
			}
		}
		if idx < 0 && fallbackFirst {
			// Nothing the caller said discriminates, so take the composite
			// listing over an arbitrary venue: it is the consolidated line for
			// the market, and recording it leaves the exchange unset rather than
			// asserting whichever venue the provider happened to list first.
			idx = 0
			for i := range results {
				if isComposite(&results[i]) {
					idx = i
					break
				}
			}
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
		// The OPENFIGI_TICKER hint is first because tryOpenFIGIFromHints takes
		// the first hint that returns anything: the venue-constrained query has
		// to run before the worldwide one, not after it.
		inst.UnderlyingIdentifiers = []identifier.Identifier{
			{Type: "OPENFIGI_TICKER", Domain: identifier.USComposite, Value: parsed.Symbol},
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
			job.Currency = toOpenFIGICurrency(hints.Currency)
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
