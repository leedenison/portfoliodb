package description

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and description_plugin_config.
const PluginID = "openai"

// Counter names for path-specific OpenAI errors and token usage (prefixed by telemetry in production).
const (
	CounterModelNotFound    = "description.openai.model_not_found"
	CounterQuotaExceeded   = "description.openai.quota_exceeded"
	CounterPromptTokens    = "description.openai.prompt_tokens"
	CounterCompletionTokens = "description.openai.completion_tokens"
	CounterTotalTokens     = "description.openai.total_tokens"
)

// configJSON is the shape of the plugin's config from description_plugin_config.config.
type configJSON struct {
	OpenAIAPIKey  string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"` // for testing
}

// Plugin implements description.Plugin using OpenAI to normalize broker descriptions to a specific identifier (ticker, ISIN, or CUSIP).
type Plugin struct {
	client  *Client
	config  configJSON
	counter telemetry.CounterIncrementer
	log     *slog.Logger
}

// NewPlugin returns a new description plugin. Counter and log are optional (nil for tests); when set, model-not-found and quota-exceeded errors are logged and counted.
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger) *Plugin {
	return &Plugin{counter: counter, log: log}
}

// DisplayName returns a human-readable name for the plugin.
func (p *Plugin) DisplayName() string {
	return "OpenAI"
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

// Extract normalizes the broker description via OpenAI and returns identifier hints (TICKER and SHARE_CLASS_FIGI).
// The model returns JSON with both; we return both to the caller so the resolver can validate they match and prefer TICKER on mismatch.
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
	norm, usage, err := p.client.NormalizeDescription(ctx, instrumentDescription)
	if err != nil || norm == nil {
		if err != nil {
			p.handleOpenAIError(ctx, instrumentDescription, err)
		}
		return nil, nil
	}
	if p.counter != nil && usage != nil {
		p.counter.IncrBy(ctx, CounterPromptTokens, usage.PromptTokens)
		p.counter.IncrBy(ctx, CounterCompletionTokens, usage.CompletionTokens)
		p.counter.IncrBy(ctx, CounterTotalTokens, usage.TotalTokens)
	}
	var hints []identifier.Identifier
	if norm.Ticker != "" {
		hints = append(hints, identifier.Identifier{Type: "TICKER", Domain: "", Value: norm.Ticker})
	}
	if norm.ShareClassFIGI != "" {
		hints = append(hints, identifier.Identifier{Type: "SHARE_CLASS_FIGI", Domain: "", Value: norm.ShareClassFIGI})
	}
	if len(hints) == 0 {
		return nil, nil
	}
	return hints, nil
}

// handleOpenAIError logs and increments path-specific counters for model-not-found and quota-exceeded errors.
func (p *Plugin) handleOpenAIError(ctx context.Context, instrumentDescription string, err error) {
	if p.log == nil && p.counter == nil {
		return
	}
	errStr := err.Error()
	if isOpenAIModelNotFound(errStr) {
		if p.log != nil {
			p.log.ErrorContext(ctx, "OpenAI description plugin: model not found", "instrument_description", instrumentDescription, "err", err)
		}
		if p.counter != nil {
			p.counter.Incr(ctx, CounterModelNotFound)
		}
		return
	}
	if isOpenAIQuotaExceeded(errStr) {
		if p.log != nil {
			p.log.ErrorContext(ctx, "OpenAI description plugin: quota exceeded", "instrument_description", instrumentDescription, "err", err)
		}
		if p.counter != nil {
			p.counter.Incr(ctx, CounterQuotaExceeded)
		}
	}
}

func isOpenAIModelNotFound(errStr string) bool {
	s := strings.ToLower(errStr)
	return strings.Contains(errStr, "404") ||
		strings.Contains(s, "model_not_found") ||
		(strings.Contains(s, "model") && strings.Contains(s, "not found"))
}

func isOpenAIQuotaExceeded(errStr string) bool {
	s := strings.ToLower(errStr)
	return strings.Contains(errStr, "429") ||
		strings.Contains(s, "insufficient_quota") ||
		strings.Contains(s, "quota")
}
