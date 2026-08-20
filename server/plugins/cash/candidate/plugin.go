package candidate

import (
	"context"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
)

// PluginID is the stable plugin_id for registration and candidate plugin config.
const PluginID = "cash"

// Plugin implements candidate.Plugin for Cash: returns the currency hint as a CURRENCY identifier.
// No external calls; no config required.
type Plugin struct{}

// NewPlugin returns a new cash candidate plugin.
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

// ProposeBatch returns one CURRENCY identifier per item when Hints.Currency is set (from tx.trading_currency).
func (p *Plugin) ProposeBatch(ctx context.Context, config []byte, broker, source string, items []candpkg.BatchItem) (candpkg.Result, error) {
	out := make(map[string][]candpkg.Proposal)
	for _, item := range items {
		code := strings.ToUpper(strings.TrimSpace(item.Hints.Currency))
		if code == "" {
			continue
		}
		// Confidence is 1: the currency is read off the transaction rather than
		// guessed, so there is nothing here for a confidence to express doubt about.
		out[item.ID] = []candpkg.Proposal{{
			Field:      candpkg.FieldCurrency,
			Identifier: identifier.Identifier{Type: "CURRENCY", Domain: "", Value: code},
			Confidence: 1,
		}}
	}
	// Tokens stay nil: the currency comes off the transaction, at no cost.
	if len(out) == 0 {
		return candpkg.Result{Telemetry: candpkg.Telemetry{Outcome: candpkg.OutcomeNoHints}}, nil
	}
	return candpkg.Result{Proposed: out, Telemetry: candpkg.Telemetry{Outcome: candpkg.OutcomeHintsReturned}}, nil
}
