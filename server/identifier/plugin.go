package identifier

import (
	"context"
	"errors"
)

// ErrNotIdentified is returned by a plugin when it cannot resolve the given (source, instrument_description).
// The service then ensures a broker-description-only instrument exists and links the tx.
var ErrNotIdentified = errors.New("instrument not identified by plugin")

// Plugin is the instrument identification plugin interface.
// Implementations live under server/plugins/<datasource>/identifier (e.g. server/plugins/local/identifier).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenFIGI"). Shown in the admin UI.
	DisplayName() string

	// AcceptableSecurityTypes returns the security types this plugin can attempt identification for (e.g. "Equity", "Bond").
	// Values must match the ingestion layer vocabulary. Nil or empty means all types.
	AcceptableSecurityTypes() []string

	// Identify resolves to canonical instrument data and identifiers. When identifierHints is non-empty, resolution is from those hints (e.g. mapping by TICKER/FIGI); when empty, the plugin may use instrumentDescription only if it can do so safely (e.g. no raw search with long text).
	// config is the plugin's JSON config from identifier_plugin_config.config (may be nil).
	// Returns (instrument, identifiers, nil) when resolved, or (nil, nil, ErrNotIdentified) when the plugin cannot resolve.
	// hints are optional (exchange, currency, MIC, security type) and must not be stored as canonical—only API-confirmed data is written to the instrument.
	// The caller ensures identifiers include at least (Type=BROKER_DESCRIPTION, Domain=source, Value=instrument_description) when creating a new instrument from description path.
	Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints Hints, identifierHints []Identifier) (*Instrument, []Identifier, error)

	// DefaultConfig returns the plugin's default config JSON (keys the plugin uses, with dummy/empty values).
	// The server calls this on startup when no row exists for the plugin and inserts the result so the user can edit it via the Admin UI. Return nil or empty slice to insert {}.
	DefaultConfig() []byte
}

// PluginConfig is the per-plugin configuration stored in the DB.
// Precedence is required and unique across plugins; higher precedence wins when merging multi-plugin results.
type PluginConfig struct {
	PluginID   string
	Enabled    bool
	Precedence int   // required, unique; higher = wins on conflict
	Config     []byte // plugin-specific JSON; may be nil
}
