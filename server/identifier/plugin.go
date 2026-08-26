package identifier

import (
	"context"
	"errors"
)

// ErrNotIdentified is returned by a plugin when it cannot resolve the given (source, instrument_description).
// The service then ensures a broker-description-only instrument exists and links the tx.
var ErrNotIdentified = errors.New("instrument not identified by plugin")

// Outcome is how one Identify call went on the wire. It is the plugin's half of
// an identifier_plugin_call row: the resolver decides afterwards whether an
// identifying plugin won, was superseded by a better hint match, or was
// discarded as inconsistent with the winner, none of which a plugin can know.
type Outcome string

const (
	// OutcomeIdentified means the plugin resolved the instrument. The resolver
	// refines this into won, superseded or discarded_inconsistent.
	OutcomeIdentified Outcome = "identified"
	// OutcomeNotIdentified means the upstream service answered and had nothing.
	OutcomeNotIdentified Outcome = "not_identified"
	// OutcomeRateLimited means the upstream service refused the call as too frequent.
	OutcomeRateLimited Outcome = "rate_limited"
	// OutcomeTimeout means the per-plugin deadline expired.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeError covers every other transport or decoding failure.
	OutcomeError Outcome = "error"
	// OutcomeSkippedExpired means the plugin declined to call upstream because
	// the derivative expired beyond the horizon it is configured to look back over.
	OutcomeSkippedExpired Outcome = "skipped_expired"
)

// Telemetry is what only the plugin knows about one Identify call. Retries and
// duration are not here: the retry loop and the clock belong to the resolver.
type Telemetry struct {
	Outcome Outcome
}

// Result is what Identify returns. Telemetry is populated on every path,
// including the error paths, where it carries the most.
type Result struct {
	Instrument *Instrument
	// Identifiers is what the call returned, and the only part of the answer
	// that is stored.
	Identifiers []Identifier
	// Filtered is what the call was strictly filtered on: values the request
	// constrained the provider to, where an empty match would have been
	// answered "no identifier found". Answering at all is the provider
	// asserting that the filtered value denotes the security it described, so a
	// filtered identifier is graded with a returned one when deciding whether
	// an association was claimed -- which matters because a provider may
	// deliberately decline to echo a matched value back.
	//
	// Strictly is the whole of it. A filter the provider silently relaxes when
	// it matches nothing is a hint, and a response to one confirms nothing --
	// it is the echo a real filter merely resembles. Nothing can check a
	// provider's strictness from outside, so a plugin populating this must say
	// at the site which request made the value a filter, exactly as it must for
	// a confirmed instrument field.
	//
	// Set it only for identifier-scope claims. A filter on the currency
	// constrains what the security is, not which security it is, and belongs on
	// the Instrument where confirmedFields already grades it.
	//
	// See adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md
	// and adr/0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md.
	Filtered  []Identifier
	Telemetry Telemetry
}

// Plugin is the instrument identification plugin interface.
// Implementations live under server/plugins/<datasource>/identifier (e.g. server/plugins/local/identifier).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenFIGI"). Shown in the admin UI.
	DisplayName() string

	// AcceptableSecurityTypes returns the asset classes this plugin can attempt
	// identification for. Keys must be from the identifier package constants
	// (SecurityTypeHintStock, etc.); nil or empty map means all of them.
	//
	// Declare the classes the plugin actually covers rather than every class a
	// source might spell them as. A plugin is offered any row whose stated class
	// could be one of these -- see pluginutil.AcceptsSecurityType -- so a plugin
	// covering shares declares STOCK and is offered the EQUITY a statement line
	// says, and a cash plugin declares CASH and is offered nothing else.
	AcceptableSecurityTypes() map[string]bool

	// Identify resolves to canonical instrument data and identifiers. When ident.Stated is non-empty, resolution is from those identifiers (e.g. mapping by TICKER/FIGI); when empty, the plugin may use instrumentDescription only if it can do so safely (e.g. no raw search with long text).
	// config is the plugin's JSON config from identifier_plugin_config.config (may be nil).
	// Returns (Result with Instrument and Identifiers, nil) when resolved, or (Result, ErrNotIdentified) when the plugin cannot resolve.
	// Result.Telemetry is set on every path, including errors, and is the plugin's contribution to the identifier_plugin_call row the resolver writes.
	// Nothing in ident may be stored as canonical -- only API-confirmed data is written to the instrument.
	//
	// ident.Proposed is what another plugin offered to fill a gap, and it is not
	// evidence. A plugin may narrow or rank with it -- preferring the result on a
	// proposed venue over one elsewhere in the world is the point of passing it --
	// but must not return it, because the resolver stores what a plugin returns and
	// a proposal that came back would be indistinguishable from a confirmed name.
	// Query with it freely; a provider that answers a query about a proposed value
	// has confirmed it, and that confirmation is the response, not the value.
	//
	// The caller ensures identifiers include at least (Type=BROKER_DESCRIPTION, Domain=source, Value=instrument_description) when creating a new instrument from description path.
	Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident Identity) (Result, error)

	// DefaultConfig returns the plugin's default config JSON (keys the plugin uses, with dummy/empty values).
	// The server calls this on startup when no row exists for the plugin and inserts the result so the user can edit it via the Admin UI. Return nil or empty slice to insert {}.
	DefaultConfig() []byte
}

// PluginConfig is the per-plugin configuration stored in the DB.
// Precedence is required and unique across plugins; higher precedence wins when merging multi-plugin results.
type PluginConfig struct {
	PluginID   string
	Enabled    bool
	Precedence int    // required, unique; higher = wins on conflict
	Config     []byte // plugin-specific JSON; may be nil
}
