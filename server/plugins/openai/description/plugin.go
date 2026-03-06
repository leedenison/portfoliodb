package description

import (
	"context"
	"encoding/json"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// PluginID is the stable plugin_id for registration and description_plugin_config.
const PluginID = "openai"

// configJSON is the shape of the plugin's config from description_plugin_config.config.
type configJSON struct {
	OpenAIAPIKey  string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"` // for testing
}

// Plugin implements description.Plugin using OpenAI to normalize broker descriptions to a specific identifier (ticker, ISIN, or CUSIP).
type Plugin struct {
	client *Client
	config configJSON
}

// NewPlugin returns a new description plugin.
func NewPlugin() *Plugin {
	return &Plugin{}
}

// DefaultConfig returns default config JSON with the keys the plugin uses.
func (p *Plugin) DefaultConfig() []byte {
	cfg := configJSON{
		OpenAIAPIKey:  "",
		OpenAIModel:   "",
		OpenAIBaseURL: "",
	}
	out, _ := json.Marshal(cfg)
	return out
}

// Extract normalizes the broker description via OpenAI and returns a single identifier hint.
// The model returns a structured "TYPE: VALUE" response; we preserve Type and Value through to the caller (identifier plugins).
func (p *Plugin) Extract(ctx context.Context, config []byte, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string) ([]identifier.Identifier, error) {
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, err
		}
	}
	p.config = cfg
	if cfg.OpenAIAPIKey == "" {
		return nil, nil
	}
	p.client = NewClient(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAIBaseURL)
	norm, err := p.client.NormalizeDescription(ctx, instrumentDescription)
	if err != nil || norm == nil {
		return nil, nil
	}
	return []identifier.Identifier{{Type: norm.Type, Domain: "", Value: norm.Value}}, nil
}
