package description

import (
	"context"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// Plugin is the description plugin interface. Description plugins extract
// identifier hints (type, domain, value) from a raw broker instrument description.
// Implementations live under server/plugins/<datasource>/description (e.g. server/plugins/openai/description).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenAI"). Shown in the admin UI.
	DisplayName() string

	// Extract parses the broker instrument description and returns zero or more identifier hints.
	// config is the plugin's JSON config from description_plugin_config.config (may be nil).
	// Hints (exchangeCodeHint, currencyHint, micHint) are optional and may be used to narrow or disambiguate extraction.
	// Returns (nil, nil) or ([]Identifier{}, nil) when nothing could be extracted; no DB access.
	Extract(ctx context.Context, config []byte, broker, source, instrumentDescription, exchangeCodeHint, currencyHint, micHint string) ([]identifier.Identifier, error)

	// DefaultConfig returns the plugin's default config JSON. The server calls this on startup when no row exists.
	DefaultConfig() []byte
}
