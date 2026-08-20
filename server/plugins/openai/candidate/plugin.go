package candidate

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
)

// PluginID is the stable plugin_id for registration and candidate plugin config.
const PluginID = "openai"

// configJSON is the shape of the plugin's config from candidate plugin config.
type configJSON struct {
	OpenAIAPIKey        string `json:"openai_api_key"`
	OpenAIModel         string `json:"openai_model"`
	OpenAIBaseURL       string `json:"openai_base_url"` // for testing
	BatchChunkSize      int    `json:"batch_chunk_size"`
	MaxCompletionTokens int    `json:"max_completion_tokens"`
}

// Plugin implements candidate.Plugin using OpenAI to normalize broker descriptions to a specific identifier (ticker, ISIN, or CUSIP).
type Plugin struct {
	client     *Client
	config     configJSON
	log        *slog.Logger
	httpClient *http.Client
}

// NewPlugin returns a new candidate plugin. log is optional (nil for tests); when set, model-not-found and quota-exceeded errors are logged.
func NewPlugin(log *slog.Logger, httpClient *http.Client) *Plugin {
	return &Plugin{log: log, httpClient: httpClient}
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

// AcceptableInstrumentKinds returns only Security.
func (p *Plugin) AcceptableInstrumentKinds() map[string]bool {
	return map[string]bool{identifier.InstrumentKindSecurity: true}
}

// AcceptableSecurityTypes returns the security type hints this plugin can
// attempt extraction for (STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION; not
// CASH or UNKNOWN). An ETF description names a ticker exactly as a stock's does,
// and the set is exclusive -- ShouldAttemptPlugin drops any item whose type is
// absent from it -- so leaving ETF out sent every ETF that arrived without
// identifiers straight to broker-description-only, with no plugin having
// declined it.
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintStock:       true,
		identifier.SecurityTypeHintETF:         true,
		identifier.SecurityTypeHintFixedIncome: true,
		identifier.SecurityTypeHintMutualFund:  true,
		identifier.SecurityTypeHintOption:      true,
	}
}

// ProposeBatch implements candpkg.Plugin. Chunks items into groups of 50 and calls the API per chunk; merges results keyed by ID.
// An API failure is absorbed -- no hints and no error, so the caller moves on to the next plugin -- and reported as the outcome it was.
func (p *Plugin) ProposeBatch(ctx context.Context, config []byte, broker, source string, items []candpkg.BatchItem) (candpkg.Result, error) {
	var cfg configJSON
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return result(nil, candpkg.OutcomeError, nil), err
		}
	}
	p.config = cfg
	if cfg.OpenAIAPIKey == "" {
		// Enabled but unconfigured: no call was made and none could be. It is
		// an error rather than an empty answer, or a plugin nobody finished
		// setting up reads as one that never finds anything.
		return result(nil, candpkg.OutcomeError, nil), nil
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
	byID, usage, err := p.client.NormalizeDescriptionsBatch(ctx, clientItems)
	if err != nil {
		outcome := candpkg.OutcomeError
		for _, item := range items {
			if o := p.handleOpenAIError(ctx, item.InstrumentDescription, err); o != candpkg.OutcomeError {
				outcome = o
			}
		}
		return result(nil, outcome, nil), nil
	}
	out := make(map[string][]candpkg.Proposal)
	for id, norm := range byID {
		if norm == nil {
			continue
		}
		// No confidence yet: the prompt asks for a bare value and the model has
		// no way to qualify it. Structured outputs give it one (0133), and until
		// then a zero says "the plugin did not report", not "no confidence".
		if norm.OCC != "" {
			out[id] = []candpkg.Proposal{{Field: candpkg.FieldKey, Identifier: identifier.Identifier{Type: "OCC", Domain: "", Value: norm.OCC}}}
		} else if norm.Ticker != "" {
			out[id] = []candpkg.Proposal{{Field: candpkg.FieldTicker, Identifier: identifier.Identifier{Type: "MIC_TICKER", Domain: "", Value: norm.Ticker}}}
		}
	}
	outcome := candpkg.OutcomeNoHints
	if len(out) > 0 {
		outcome = candpkg.OutcomeHintsReturned
	}
	return result(out, outcome, usage), nil
}

// result assembles the extraction and the telemetry for the call. usage may be
// nil, in which case the call cost no tokens the plugin could observe.
func result(proposed map[string][]candpkg.Proposal, outcome candpkg.Outcome, usage *Usage) candpkg.Result {
	res := candpkg.Result{Proposed: proposed, Telemetry: candpkg.Telemetry{Outcome: outcome}}
	if usage != nil {
		res.Telemetry.Tokens = &candpkg.Usage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		}
	}
	return res
}

// handleOpenAIError logs the errors worth naming and classifies one into the
// outcome vocabulary. Returns OutcomeError for anything it does not recognise.
func (p *Plugin) handleOpenAIError(ctx context.Context, instrumentDescription string, err error) candpkg.Outcome {
	errStr := err.Error()
	if isOpenAIModelNotFound(errStr) {
		if p.log != nil {
			p.log.ErrorContext(ctx, "OpenAI candidate plugin: model not found", "instrument_description", instrumentDescription, "err", err)
		}
		return candpkg.OutcomeModelNotFound
	}
	if isOpenAIRateLimited(errStr) {
		if p.log != nil {
			p.log.ErrorContext(ctx, "OpenAI candidate plugin: rate limited", "instrument_description", instrumentDescription, "err", err)
		}
		return candpkg.OutcomeRateLimited
	}
	if isOpenAIQuotaExceeded(errStr) {
		if p.log != nil {
			p.log.ErrorContext(ctx, "OpenAI candidate plugin: quota exceeded", "instrument_description", instrumentDescription, "err", err)
		}
		return candpkg.OutcomeQuotaExceeded
	}
	return candpkg.OutcomeError
}

// isOpenAIRateLimited matches the rate limit OpenAI reports with the same 429
// it uses for an exhausted quota, so it is checked by the body marker and
// tried before the quota test.
func isOpenAIRateLimited(errStr string) bool {
	s := strings.ToLower(errStr)
	return strings.Contains(s, "rate_limit_exceeded") || strings.Contains(s, "rate limit")
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
