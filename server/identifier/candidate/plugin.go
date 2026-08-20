package candidate

import (
	"context"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// Outcome is how one ProposeBatch call went. It is the whole of a description
// plugin call's outcome: unlike identifier plugins, candidate plugins run in
// series and the caller adds nothing to what the plugin reports.
type Outcome string

const (
	// OutcomeHintsReturned means the plugin extracted hints for at least one item.
	OutcomeHintsReturned Outcome = "hints_returned"
	// OutcomeNoHints means the call succeeded and extracted nothing.
	OutcomeNoHints Outcome = "no_hints"
	// OutcomeError covers transport, decoding and API errors.
	OutcomeError Outcome = "error"
	// OutcomeRateLimited means the upstream service refused the call as too frequent.
	OutcomeRateLimited Outcome = "rate_limited"
	// OutcomeQuotaExceeded means the account has no quota left.
	OutcomeQuotaExceeded Outcome = "quota_exceeded"
	// OutcomeModelNotFound means the configured model does not exist.
	OutcomeModelNotFound Outcome = "model_not_found"
)

// Usage is the token cost of one call, summed over the chunks a plugin split
// the batch into. Nil for plugins that cost no tokens, which is what keeps the
// token columns null rather than zero for them.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Telemetry is what only the plugin knows about one ProposeBatch call. The
// batch size and the number of items that came back with hints are the
// caller's to count.
type Telemetry struct {
	Outcome Outcome
	Tokens  *Usage
}

// Field names the part of an instrument's identity a proposal fills in. The
// identifier alone does not say: proposing the venue and proposing the symbol
// both arrive as a MIC_TICKER, one carrying a domain and one a value, and
// telling them apart is what lets accuracy be reported per field rather than
// per plugin.
const (
	FieldTicker   = "ticker"
	FieldExchange = "exchange"
	FieldCurrency = "currency"
	FieldKey      = "key" // an ISIN, CUSIP or other opaque identifier
)

// Proposal is one thing a plugin offers to fill in, and the reason Result is not
// simply a list of identifiers: a proposal is a claim with a provenance and a
// self-reported confidence, and the resolver has to be able to say which field
// it was about when it records what became of it.
//
// Confidence is what the plugin says about its own answer, on [0, 1]. It is
// recorded and never gated on: a model's self-report is uncalibrated, and
// turning it into a threshold before anything has measured whether it
// correlates with correctness would be inventing a number. What decides whether
// a proposal is used is the resolution, not this.
type Proposal struct {
	Field      string
	Identifier identifier.Identifier
	Confidence float64
}

// Result is what ProposeBatch returns. Proposed is keyed by BatchItem.ID; items
// the plugin had nothing to add for may be absent or carry an empty slice.
// Telemetry is populated on every path, including the error paths.
type Result struct {
	Proposed  map[string][]Proposal
	Telemetry Telemetry
}

// BatchItem is one item for a batch. ID is a short stable key (e.g. hash) used
// to match responses.
//
// Stated is what the source already said about this instrument -- the
// identifiers a broker file carried, or a converter read out of one. A plugin is
// given it so it can fill in what is missing rather than repeat what is known,
// and so it can use a known ISIN to work out the ticker the file left out. It is
// evidence, and a plugin must not contradict it: what comes back is a proposal
// about the gaps. See adr/0057.
type BatchItem struct {
	ID                    string
	InstrumentDescription string
	Stated                []identifier.Identifier
	Hints                 identifier.Hints
}

// Plugin is the candidate plugin interface. Candidate plugins extract
// identifier hints (type, domain, value) from raw broker instrument descriptions.
// Implementations live under server/plugins/<datasource>/description (e.g. server/plugins/openai/candidate).
// Callers always use ProposeBatch (with a single BatchItem when resolving one description).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenAI"). Shown in the admin UI.
	DisplayName() string

	// AcceptableInstrumentKinds returns the set of instrument kinds this plugin handles (identifier.InstrumentKindCash, identifier.InstrumentKindSecurity).
	// Nil or empty map means all kinds. Checked before AcceptableSecurityTypes as a coarse filter.
	AcceptableInstrumentKinds() map[string]bool

	// AcceptableSecurityTypes returns the set of security type hints this plugin can attempt extraction for (e.g. Stock, Bond).
	// Keys must be from the identifier package constants (SecurityTypeHintStock, etc.). Nil or empty map means all types.
	AcceptableSecurityTypes() map[string]bool

	// ProposeBatch runs over all items. config is the plugin's JSON config (may be nil).
	// Returns a Result whose Proposed are keyed by BatchItem.ID, empty when the plugin had nothing to add; no DB access.
	// Result.Telemetry is set on every path and is the plugin's contribution to the description_plugin_call row the caller writes.
	ProposeBatch(ctx context.Context, config []byte, broker, source string, items []BatchItem) (Result, error)

	// DefaultConfig returns the plugin's default config JSON. The server calls this on startup when no row exists.
	DefaultConfig() []byte
}
