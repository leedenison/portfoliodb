package description

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/telemetry"
)

// PluginID is the stable plugin_id for registration and description_plugin_config.
const PluginID = "openai"

// Counter names (prefixed by telemetry in production).
const (
	CounterAttempted        = "instruments.description.openai.ticker_extraction.attempted"
	CounterSucceeded        = "instruments.description.openai.ticker_extraction.succeeded"
	CounterFailed           = "instruments.description.openai.ticker_extraction.failed"
	CounterModelNotFound    = "instruments.description.openai.ticker_extraction.model_not_found"
	CounterQuotaExceeded    = "instruments.description.openai.ticker_extraction.quota_exceeded"
	CounterPromptTokens     = "instruments.description.openai.ticker_extraction.prompt_tokens"
	CounterCompletionTokens = "instruments.description.openai.ticker_extraction.completion_tokens"
	CounterTotalTokens      = "instruments.description.openai.ticker_extraction.total_tokens"
)

// configJSON is the shape of the plugin's config from description_plugin_config.config.
type configJSON struct {
	OpenAIAPIKey         string `json:"openai_api_key"`
	OpenAIModel          string `json:"openai_model"`
	OpenAIBaseURL        string `json:"openai_base_url"` // for testing
	BatchChunkSize       int    `json:"batch_chunk_size"`
	MaxCompletionTokens  int    `json:"max_completion_tokens"`
}

// Plugin implements description.Plugin using OpenAI to normalize broker descriptions to a specific identifier (ticker, ISIN, or CUSIP).
type Plugin struct {
	client     *Client
	config     configJSON
	counter    telemetry.CounterIncrementer
	log        *slog.Logger
	httpClient *http.Client
}

// NewPlugin returns a new description plugin. Counter and log are optional (nil for tests); when set, model-not-found and quota-exceeded errors are logged and counted.
func NewPlugin(counter telemetry.CounterIncrementer, log *slog.Logger, httpClient *http.Client) *Plugin {
	return &Plugin{counter: counter, log: log, httpClient: httpClient}
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

// AcceptableSecurityTypes returns the security type hints this plugin can attempt extraction for (STOCK, FIXED_INCOME, MUTUAL_FUND, OPTION; not CASH or UNKNOWN).
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock:       true,
		identifier.SecurityTypeHintFixedIncome: true,
		identifier.SecurityTypeHintMutualFund:  true,
		identifier.SecurityTypeHintOption:     true,
	}
}

// ExtractBatch implements descpkg.Plugin. Chunks items into groups of 50 and calls the API per chunk; merges results keyed by ID.
func (p *Plugin) ExtractBatch(ctx context.Context, config []byte, broker, source string, items []descpkg.BatchItem) (map[string][]identifier.Identifier, error) {
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
	p.client = NewClient(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAIBaseURL, cfg.BatchChunkSize, cfg.MaxCompletionTokens, p.httpClient)
	clientItems := make([]BatchItemForClient, len(items))
	for i := range items {
		clientItems[i] = BatchItemForClient{
			ID:          items[i].ID,
			Description: items[i].InstrumentDescription,
			TypeHint:    items[i].Hints.SecurityTypeHint,
		}
	}
	if p.counter != nil {
		p.counter.IncrBy(ctx, CounterAttempted, int64(len(items)))
	}
	byID, usage, err := p.client.NormalizeDescriptionsBatch(ctx, clientItems)
	if err != nil {
		if p.counter != nil {
			p.counter.IncrBy(ctx, CounterFailed, int64(len(items)))
		}
		for _, item := range items {
			p.handleOpenAIError(ctx, item.InstrumentDescription, err)
		}
		return nil, nil
	}
	if p.counter != nil && usage != nil {
		p.counter.IncrBy(ctx, CounterPromptTokens, usage.PromptTokens)
		p.counter.IncrBy(ctx, CounterCompletionTokens, usage.CompletionTokens)
		p.counter.IncrBy(ctx, CounterTotalTokens, usage.TotalTokens)
	}
	out := make(map[string][]identifier.Identifier)
	for id, norm := range byID {
		if norm == nil {
			continue
		}
		if norm.OCC != "" {
			out[id] = []identifier.Identifier{{Type: "OCC", Domain: "", Value: norm.OCC}}
		} else if norm.Ticker != "" {
			out[id] = []identifier.Identifier{{Type: "TICKER", Domain: "", Value: norm.Ticker}}
		}
	}
	if p.counter != nil {
		succeeded := int64(len(out))
		failed := int64(len(items)) - succeeded
		p.counter.IncrBy(ctx, CounterSucceeded, succeeded)
		if failed > 0 {
			p.counter.IncrBy(ctx, CounterFailed, failed)
		}
	}
	return out, nil
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
