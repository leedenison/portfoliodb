package identifier

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
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
	OpenAIAPIKey    string `json:"openai_api_key"`
	OpenAIModel     string `json:"openai_model"`
	OpenFIGIBaseURL string `json:"openfigi_base_url"` // for testing
	OpenAIBaseURL   string `json:"openai_base_url"`  // for testing
}

// Plugin implements identifier.Plugin using OpenFIGI and optionally OpenAI.
type Plugin struct {
	openfigi *OpenFIGIClient
	openai   *OpenAIClient
	config   configJSON
	counter  telemetry.CounterIncrementer
	log      *slog.Logger
}

// NewPlugin returns a plugin. Counter and log are optional (nil for tests); when set, OpenFIGI calls are counted and logged.
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger) *Plugin {
	return &Plugin{counter: counter, log: log}
}

// DefaultConfig returns default config JSON with the keys the plugin uses and empty/dummy values for the user to fill in via Admin UI.
func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{
		OpenFIGIAPIKey:  "",
		OpenAIAPIKey:    "",
		OpenAIModel:     "",
		OpenFIGIBaseURL: "",
		OpenAIBaseURL:   "",
	}
	out, _ := json.Marshal(cfg)
	return out
}

// Identify resolves (source, instrument_description) using OpenFIGI first, then OpenAI if needed.
// exchangeCodeHint narrows mapping/search when provided; only API-confirmed exchange is stored on the instrument.
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription, exchangeCodeHint string) (*identifier.Instrument, []identifier.Identifier, error) {
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
	if cfg.OpenAIAPIKey != "" {
		if cfg.OpenAIBaseURL != "" {
			p.openai = NewOpenAIClientWithBaseURL(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAIBaseURL)
		} else {
			p.openai = NewOpenAIClient(cfg.OpenAIAPIKey, cfg.OpenAIModel)
		}
	} else {
		p.openai = nil
	}

	// 1) Try OpenFIGI first (mapping if looks like ticker, else search)
	results, err := p.tryOpenFIGI(ctx, instrumentDescription, exchangeCodeHint)
	if err != nil {
		return nil, nil, err
	}
	if inst, ids, ok := p.resolveResults(ctx, results, false); ok {
		return inst, ids, nil
	}

	// 2) ChatGPT only when needed
	if p.openai == nil {
		return nil, nil, identifier.ErrNotIdentified
	}
	normalized, err := p.openai.NormalizeDescription(ctx, instrumentDescription)
	if err != nil || normalized == "" {
		return nil, nil, identifier.ErrNotIdentified
	}
	results, err = p.tryOpenFIGI(ctx, normalized, exchangeCodeHint)
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

var tickerLikeRe = regexp.MustCompile(`^[A-Z0-9]{1,5}$`)

func (p *Plugin) tryOpenFIGI(ctx context.Context, query, exchangeCodeHint string) ([]OpenFIGIResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, identifier.ErrNotIdentified
	}
	upper := strings.ToUpper(query)
	if tickerLikeRe.MatchString(upper) {
		job := MappingJob{IDType: "TICKER", IDValue: upper}
		if exchangeCodeHint != "" {
			job.ExchCode = exchangeCodeHint
		}
		results, err := p.openfigi.Mapping(ctx, job)
		if err != nil {
			return nil, err
		}
		return results, nil
	}
	sr, err := p.openfigi.Search(ctx, query, exchangeCodeHint)
	if err != nil {
		return nil, err
	}
	if sr == nil {
		return nil, nil
	}
	return sr.Data, nil
}

// getUnderlyingSymbol resolves the underlying ticker for a derivative using the derivative library, then OpenAI if needed.
// It does not assume the underlying trades on the same exchange as the derivative.
func (p *Plugin) getUnderlyingSymbol(ctx context.Context, derivativeTicker string) (symbol, exchangeHint string, ok bool) {
	if u, parseOk := derivative.ParseOptionTicker(derivativeTicker); parseOk {
		return u.Symbol, u.ExchangeHint, true
	}
	if p.openai != nil {
		sym, err := p.openai.UnderlyingFromDerivative(ctx, derivativeTicker)
		if err == nil && strings.TrimSpace(sym) != "" {
			return strings.TrimSpace(sym), "", true
		}
	}
	return "", "", false
}
