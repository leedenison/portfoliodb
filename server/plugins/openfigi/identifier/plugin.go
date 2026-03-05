package identifier

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "openfigi"

// configJSON is the shape of the plugin's config from identifier_plugin_config.config.
type configJSON struct {
	OpenFIGIAPIKey   string `json:"openfigi_api_key"`
	OpenAIAPIKey     string `json:"openai_api_key"`
	ExchCode         string `json:"exch_code"`
	OpenAIModel      string `json:"openai_model"`
	OpenFIGIBaseURL  string `json:"openfigi_base_url"`  // for testing
	OpenAIBaseURL    string `json:"openai_base_url"`    // for testing
}

// Plugin implements identifier.Plugin using OpenFIGI and optionally OpenAI.
type Plugin struct {
	openfigi *OpenFIGIClient
	openai   *OpenAIClient
	config   configJSON
}

// NewPlugin returns a plugin that uses the given clients. Config is parsed from the passed config bytes when Identify is called.
func NewPlugin() *Plugin {
	return &Plugin{}
}

// DefaultConfig returns default config JSON with the keys the plugin uses and empty/dummy values for the user to fill in via Admin UI.
func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{
		OpenFIGIAPIKey:  "",
		OpenAIAPIKey:    "",
		ExchCode:        "",
		OpenAIModel:     "",
		OpenFIGIBaseURL: "",
		OpenAIBaseURL:   "",
	}
	out, _ := json.Marshal(cfg)
	return out
}

// Identify resolves (source, instrument_description) using OpenFIGI first, then OpenAI if needed.
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string) (*identifier.Instrument, []identifier.Identifier, error) {
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, nil, err
		}
	}
	p.config = cfg
	if cfg.OpenFIGIBaseURL != "" {
		p.openfigi = NewOpenFIGIClientWithBaseURL(cfg.OpenFIGIAPIKey, cfg.OpenFIGIBaseURL)
	} else {
		p.openfigi = NewOpenFIGIClient(cfg.OpenFIGIAPIKey)
	}
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
	results, err := p.tryOpenFIGI(ctx, instrumentDescription)
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
	results, err = p.tryOpenFIGI(ctx, normalized)
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
	EnsureUnderlying(ctx, p.openfigi, p.config.ExchCode, inst, &results[idx])
	return inst, ids, true
}

var tickerLikeRe = regexp.MustCompile(`^[A-Z0-9]{1,5}$`)

func (p *Plugin) tryOpenFIGI(ctx context.Context, query string) ([]OpenFIGIResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, identifier.ErrNotIdentified
	}
	upper := strings.ToUpper(query)
	if tickerLikeRe.MatchString(upper) {
		job := MappingJob{IDType: "TICKER", IDValue: upper}
		if p.config.ExchCode != "" {
			job.ExchCode = p.config.ExchCode
		}
		results, err := p.openfigi.Mapping(ctx, job)
		if err != nil {
			return nil, err
		}
		return results, nil
	}
	sr, err := p.openfigi.Search(ctx, query, p.config.ExchCode)
	if err != nil {
		return nil, err
	}
	if sr == nil {
		return nil, nil
	}
	return sr.Data, nil
}
