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
	// Returns (instrument, identifiers, nil) when resolved, or (nil, nil, ErrNotIdentified) when the plugin cannot resolve.
	// broker is the broker name (e.g. "IBKR", "SCHB"); source is opaque (e.g. "<broker>:<client>:<source>"); instrument_description is the broker's description string.
	// Plugins must not rely on extracting broker from source; both are passed. The caller ensures identifiers include at least (Type=source, Value=instrument_description) when creating a new instrument.
	Identify(ctx context.Context, broker, source, instrumentDescription string) (*Instrument, []Identifier, error)
}

// PluginConfig is the per-plugin configuration stored in the DB.
// Precedence is required and unique across plugins; higher precedence wins when merging multi-plugin results.
type PluginConfig struct {
	PluginID   string
	Enabled    bool
	Precedence int   // required, unique; higher = wins on conflict
	Config     []byte // plugin-specific JSON; may be nil
}
