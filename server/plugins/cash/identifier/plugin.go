package identifier

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// PluginID is the stable plugin_id for registration and identifier_plugin_config.
const PluginID = "cash"

// Plugin implements identifier.Plugin for Cash: looks up currency instruments by CURRENCY identifier (seeded at migration time).
type Plugin struct {
	database db.InstrumentDB
}

// NewPlugin returns a new cash identifier plugin. database is used to look up instruments by CURRENCY identifier.
func NewPlugin(database db.InstrumentDB) *Plugin {
	return &Plugin{database: database}
}

// configJSON is the shape of the plugin's config (empty for this plugin).
type configJSON struct{}

// DisplayName returns a human-readable name for the plugin.
func (p *Plugin) DisplayName() string {
	return "Cash"
}

// DefaultConfig returns minimal config; this plugin has no config keys.
func (p *Plugin) DefaultConfig() []byte {
	out, _ := json.Marshal(configJSON{})
	return out
}

// AcceptableSecurityTypes returns only Cash; the plugin looks up by CURRENCY identifier.
func (p *Plugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{identifier.SecurityTypeHintCash: true}
}

// Identify looks up an instrument by CURRENCY identifier. When identifierHints contain a CURRENCY type with non-empty value,
// looks up the instrument in the DB (seeded at migration). Returns ErrNotIdentified when not found or no CURRENCY hint.
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	var code string
	for _, h := range identifierHints {
		if strings.TrimSpace(h.Type) == "CURRENCY" && strings.TrimSpace(h.Value) != "" {
			code = strings.ToUpper(strings.TrimSpace(h.Value))
			break
		}
	}
	if code == "" {
		return nil, nil, identifier.ErrNotIdentified
	}
	instrumentID, err := p.database.FindInstrumentByIdentifier(ctx, "CURRENCY", "", code)
	if err != nil {
		return nil, nil, err
	}
	if instrumentID == "" {
		return nil, nil, identifier.ErrNotIdentified
	}
	row, err := p.database.GetInstrument(ctx, instrumentID)
	if err != nil || row == nil {
		return nil, nil, identifier.ErrNotIdentified
	}
	inst := &identifier.Instrument{
		ID: row.ID,
	}
	if row.AssetClass != nil {
		inst.AssetClass = *row.AssetClass
	}
	if row.ExchangeMIC != nil {
		inst.Exchange = *row.ExchangeMIC
	}
	if row.Currency != nil {
		inst.Currency = *row.Currency
	}
	if row.Name != nil {
		inst.Name = *row.Name
	}
	ids := []identifier.Identifier{{Type: "CURRENCY", Domain: "", Value: code}}
	return inst, ids, nil
}
