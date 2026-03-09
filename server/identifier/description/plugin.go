package description

import (
	"context"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// BatchItem is one item for batch extraction. ID is a short stable key (e.g. hash) used to match responses.
type BatchItem struct {
	ID                    string
	InstrumentDescription string
	Hints                 identifier.Hints
}

// Plugin is the description plugin interface. Description plugins extract
// identifier hints (type, domain, value) from raw broker instrument descriptions.
// Implementations live under server/plugins/<datasource>/description (e.g. server/plugins/openai/description).
// Callers always use ExtractBatch (with a single BatchItem when resolving one description).
type Plugin interface {
	// DisplayName returns a human-readable name for the plugin (e.g. "OpenAI"). Shown in the admin UI.
	DisplayName() string

	// AcceptableSecurityTypes returns the set of security type hints this plugin can attempt extraction for (e.g. Stock, Bond).
	// Keys must be from the identifier package constants (SecurityTypeHintStock, etc.). Nil or empty map means all types.
	AcceptableSecurityTypes() map[string]bool

	// ExtractBatch runs extraction on all items. config is the plugin's JSON config (may be nil).
	// Result map is keyed by BatchItem.ID. Returns (nil, nil) or empty map when nothing could be extracted; no DB access.
	ExtractBatch(ctx context.Context, config []byte, broker, source string, items []BatchItem) (map[string][]identifier.Identifier, error)

	// DefaultConfig returns the plugin's default config JSON. The server calls this on startup when no row exists.
	DefaultConfig() []byte
}
