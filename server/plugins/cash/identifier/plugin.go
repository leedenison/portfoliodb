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
func (p *Plugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident identifier.Identity) (identifier.Result, error) {
	identifierHints := ident.Stated
	var code string
	for _, h := range identifierHints {
		if strings.TrimSpace(h.Type) == "CURRENCY" && strings.TrimSpace(h.Value) != "" {
			code = strings.ToUpper(strings.TrimSpace(h.Value))
			break
		}
	}
	if code == "" {
		return notIdentified(), identifier.ErrNotIdentified
	}
	instrumentID, err := p.database.FindInstrumentByIdentifier(ctx, "CURRENCY", "", code)
	if err != nil {
		return identifier.Result{Telemetry: identifier.Telemetry{Outcome: identifier.OutcomeError}}, err
	}
	if instrumentID == "" {
		return notIdentified(), identifier.ErrNotIdentified
	}
	row, err := p.database.GetInstrument(ctx, instrumentID)
	if err != nil || row == nil {
		return notIdentified(), identifier.ErrNotIdentified
	}
	inst := &identifier.Instrument{
		ID: row.ID,
	}
	if row.AssetClass != nil {
		inst.AssetClass = *row.AssetClass
	}
	// A cash instrument has a listing degenerately, so it has exactly one line and
	// that line's currency is the money it is. No venue: cash trades at none.
	if len(row.Listings) == 1 {
		inst.Listing.Currency = row.Listings[0].Currency
	}
	if row.Name != nil {
		inst.Name = *row.Name
	}
	ids := []identifier.Identifier{{Type: "CURRENCY", Domain: "", Value: code}}
	return identifier.Result{
		Instrument:  inst,
		Identifiers: ids,
		Telemetry:   identifier.Telemetry{Outcome: identifier.OutcomeIdentified},
	}, nil
}

// notIdentified returns the empty result carrying the not-identified outcome.
func notIdentified() identifier.Result {
	return identifier.Result{Telemetry: identifier.Telemetry{Outcome: identifier.OutcomeNotIdentified}}
}
