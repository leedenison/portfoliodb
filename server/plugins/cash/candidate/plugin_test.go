package candidate

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
)

func TestPlugin_ProposeBatch_ReturnsCurrency(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []candpkg.BatchItem{
		{ID: "1", InstrumentDescription: "USD Cash", Hints: identifier.Hints{Currency: "USD", SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ProposeBatch(ctx, nil, "IBKR", "IBKR:test", items)
	if err != nil {
		t.Fatalf("ProposeBatch: %v", err)
	}
	if res.Telemetry.Outcome != candpkg.OutcomeHintsReturned {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, candpkg.OutcomeHintsReturned)
	}
	if res.Telemetry.Tokens != nil {
		t.Errorf("Telemetry.Tokens = %+v, want nil for a plugin with no token cost", res.Telemetry.Tokens)
	}
	hints := proposedIDs(res.Proposed["1"])
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].Type != "CURRENCY" || hints[0].Value != "USD" {
		t.Errorf("hint = %+v, want Type=CURRENCY Value=USD", hints[0])
	}
}

func TestPlugin_ProposeBatch_NormalizesCurrencyCode(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []candpkg.BatchItem{
		{ID: "1", Hints: identifier.Hints{Currency: "  usd  ", SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ProposeBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ProposeBatch: %v", err)
	}
	if proposedIDs(res.Proposed["1"])[0].Value != "USD" {
		t.Errorf("Value = %q, want USD", proposedIDs(res.Proposed["1"])[0].Value)
	}
}

func TestPlugin_ProposeBatch_EmptyCurrency_ReturnsNothing(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []candpkg.BatchItem{
		{ID: "1", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ProposeBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ProposeBatch: %v", err)
	}
	if len(res.Proposed) > 0 {
		t.Errorf("expected no hints when Currency empty, got %+v", res.Proposed)
	}
	if res.Telemetry.Outcome != candpkg.OutcomeNoHints {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, candpkg.OutcomeNoHints)
	}
}

func TestPlugin_AcceptableInstrumentKinds_OnlyCash(t *testing.T) {
	p := NewPlugin()
	set := p.AcceptableInstrumentKinds()
	if len(set) != 1 || !set[identifier.InstrumentKindCash] {
		t.Errorf("AcceptableInstrumentKinds = %v, want {CASH}", set)
	}
}

func TestPlugin_AcceptableSecurityTypes_OnlyCash(t *testing.T) {
	p := NewPlugin()
	set := p.AcceptableSecurityTypes()
	if len(set) != 1 || !set[identifier.SecurityTypeHintCash] {
		t.Errorf("AcceptableSecurityTypes = %v, want set containing %s", set, identifier.SecurityTypeHintCash)
	}
}

// proposedIDs is the identifiers out of a plugin's proposals, for assertions
// that are about the values rather than the fields they fill.
func proposedIDs(ps []candpkg.Proposal) []identifier.Identifier {
	out := make([]identifier.Identifier, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Identifier)
	}
	return out
}
