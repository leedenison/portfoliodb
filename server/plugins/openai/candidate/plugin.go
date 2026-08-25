package candidate

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/leedenison/portfoliodb/server/derivative"
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

// AcceptableSecurityTypes returns the classes this plugin can attempt
// extraction for. EQUITY covers the three below it in one entry, because a
// description names a ticker the same way whichever of them it turns out to be
// and this plugin cannot tell them apart either.
//
// Not cash: that belongs to the cash plugin, and the classes here reach no
// further across the tree.
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{
		identifier.SecurityTypeHintEquity:      true,
		identifier.SecurityTypeHintFixedIncome: true,
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
			Known:       knownFrom(items[i]),
		}
	}
	byID, usage, err := p.client.CompleteBatch(ctx, clientItems)
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
	for i := range items {
		c := byID[items[i].ID]
		if c == nil {
			continue
		}
		if ps := proposals(c, clientItems[i]); len(ps) > 0 {
			out[items[i].ID] = ps
		}
	}
	outcome := candpkg.OutcomeNoHints
	if len(out) > 0 {
		outcome = candpkg.OutcomeHintsReturned
	}
	return result(out, outcome, usage), nil
}

// knownFrom reads what the source already said about an instrument into the form
// the prompt sends. It is the identifiers the source stated, not anything a
// plugin proposed: showing the model a guess as though it were given would let
// the next guess be built on it.
func knownFrom(item candpkg.BatchItem) Known {
	k := Known{Currency: item.Hints.Currency}
	for _, id := range item.Stated {
		switch id.Type {
		case "MIC_TICKER", "OPENFIGI_TICKER":
			if k.Ticker == "" {
				k.Ticker = id.Value
			}
			if k.Exchange == "" {
				k.Exchange = id.Domain
			}
		case "ISIN":
			k.ISIN = id.Value
		case "CUSIP":
			k.CUSIP = id.Value
		case "SEDOL":
			k.SEDOL = id.Value
		case "CURRENCY":
			if k.Currency == "" {
				k.Currency = id.Value
			}
		}
	}
	return k
}

// proposals turns one completion into the fields the resolver can act on.
//
// An OCC symbol is offered alone: it names the contract, its underlying, its
// expiry and its strike at once, so a ticker or a venue beside it would be
// describing something else. An option whose OCC symbol will not parse is
// offered nothing at all, because the ticker the model returns beside it is the
// underlying -- proposing that would resolve the contract to the share.
//
// For everything else the venue travels on the ticker, because a MIC_TICKER is
// one identifier rather than two: an exchange with no symbol to qualify names
// nothing the resolver can look up.
//
// A field the source already supplied is dropped rather than passed on. The
// prompt asks for the missing fields and the model returns the known ones
// anyway -- reliably enough that filtering in prose was never going to work --
// and a proposal restating what a source said is noise the resolver would
// ignore and 0134 would have to count.
func proposals(c *Completion, item BatchItemForClient) []candpkg.Proposal {
	if item.TypeHint == identifier.SecurityTypeHintOption {
		// Validated rather than trusted: a malformed symbol is dropped here so
		// nothing downstream has to decide what to do with one. It also
		// normalises to the compact form the database stores.
		compact, ok := derivative.OCCCompact(c.OCC.Value)
		if !ok {
			return nil
		}
		return []candpkg.Proposal{{
			Field:      candpkg.FieldKey,
			Identifier: identifier.Identifier{Type: "OCC", Value: compact},
			Confidence: c.OCC.Confidence,
		}}
	}
	var out []candpkg.Proposal
	ticker, exchange := c.Ticker, c.Exchange
	if item.Known.Ticker != "" {
		ticker = Field{}
	}
	if item.Known.Exchange != "" {
		exchange = Field{}
	}
	if ticker.Value != "" {
		// The exchange is the domain, and the pair is recorded under whichever
		// half carried the weaker claim: a ticker that is right on a venue that
		// is wrong is a different failure from both being wrong, and 0134 counts
		// them apart.
		field, conf := candpkg.FieldTicker, ticker.Confidence
		if exchange.Value != "" && exchange.Confidence < conf {
			field, conf = candpkg.FieldExchange, exchange.Confidence
		}
		out = append(out, candpkg.Proposal{
			Field:      field,
			Identifier: identifier.Identifier{Type: "MIC_TICKER", Domain: exchange.Value, Value: ticker.Value},
			Confidence: conf,
		})
	}
	if c.Currency.Value != "" && item.Known.Currency == "" {
		out = append(out, candpkg.Proposal{
			Field:      candpkg.FieldCurrency,
			Identifier: identifier.Identifier{Type: "CURRENCY", Value: c.Currency.Value},
			Confidence: c.Currency.Confidence,
		})
	}
	return out
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
