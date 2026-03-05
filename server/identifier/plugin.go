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
	// Identify resolves (source, instrument_description) to canonical instrument data and identifiers.
	// config is the plugin's JSON config from identifier_plugin_config.config (may be nil); plugins may use it for API keys and options.
	// Returns (instrument, identifiers, nil) when resolved, or (nil, nil, ErrNotIdentified) when the plugin cannot resolve.
	// broker is the broker name (e.g. "IBKR", "SCHB"); source is opaque (e.g. "<broker>:<client>:<source>"); instrument_description is the broker's description string.
	// exchangeCodeHint is an optional hint from the upload (e.g. transaction) to narrow mapping/search; it must not be stored as canonical—only API-confirmed data (e.g. OpenFIGI exchCode) is written to the instrument.
	// Plugins must not rely on extracting broker from source; both are passed. The caller ensures identifiers include at least (Type=source, Value=instrument_description) when creating a new instrument.
	Identify(ctx context.Context, config []byte, broker, source, instrumentDescription, exchangeCodeHint string) (*Instrument, []Identifier, error)

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
