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
	Instrument  *Instrument
	Identifiers []Identifier
	Telemetry   Telemetry
}

// Plugin is the instrument identification plugin interface.
// Implementations live under server/plugins/<datasource>/identifier (e.g. server/plugins/local/identifier).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenFIGI"). Shown in the admin UI.
	DisplayName() string

	// AcceptableInstrumentKinds returns the set of instrument kinds this plugin handles (InstrumentKindCash, InstrumentKindSecurity).
	// Nil or empty map means all kinds. Checked before AcceptableSecurityTypes as a coarse filter.
	AcceptableInstrumentKinds() map[string]bool

	// AcceptableSecurityTypes returns the set of security type hints this plugin can attempt identification for (e.g. Stock, Bond).
	// Keys must be from the identifier package constants (SecurityTypeHintStock, etc.). Nil or empty map means all types.
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
