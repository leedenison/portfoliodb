package description

import (
	"context"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
)

// PluginID is the stable plugin_id for registration and description_plugin_config.
const PluginID = "cash"

// Plugin implements description.Plugin for Cash: returns the currency hint as a CURRENCY identifier.
// No external calls; no config required.
type Plugin struct{}

// NewPlugin returns a new cash description plugin.
func NewPlugin() *Plugin {
	return &Plugin{}
}

// DisplayName returns a human-readable name for the plugin.
func (p *Plugin) DisplayName() string {
	return "Cash"
}

// DefaultConfig returns minimal config (empty object); this plugin has no config keys.
func (p *Plugin) DefaultConfig() []byte {
	return []byte("{}")
}

// AcceptableInstrumentKinds returns only Cash.
func (p *Plugin) AcceptableInstrumentKinds() map[string]bool {
	return map[string]bool{identifier.InstrumentKindCash: true}
}

// AcceptableSecurityTypes returns only Cash; the plugin turns Hints.Currency into a CURRENCY identifier.
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{identifier.SecurityTypeHintCash: true}
}

// ExtractBatch returns one CURRENCY identifier per item when Hints.Currency is set (from tx.trading_currency).
func (p *Plugin) ExtractBatch(ctx context.Context, config []byte, broker, source string, items []descpkg.BatchItem) (map[string][]identifier.Identifier, error) {
	out := make(map[string][]identifier.Identifier)
	for _, item := range items {
		code := strings.ToUpper(strings.TrimSpace(item.Hints.Currency))
		if code == "" {
			continue
		}
		out[item.ID] = []identifier.Identifier{{Type: "CURRENCY", Domain: "", Value: code}}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
