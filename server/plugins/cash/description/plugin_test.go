package description

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
)

func TestPlugin_ExtractBatch_ReturnsCurrency(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []descpkg.BatchItem{
		{ID: "1", InstrumentDescription: "USD Cash", Hints: identifier.Hints{Currency: "USD", SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ExtractBatch(ctx, nil, "IBKR", "IBKR:test", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if res.Telemetry.Outcome != descpkg.OutcomeHintsReturned {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, descpkg.OutcomeHintsReturned)
	}
	if res.Telemetry.Tokens != nil {
		t.Errorf("Telemetry.Tokens = %+v, want nil for a plugin with no token cost", res.Telemetry.Tokens)
	}
	hints := res.Hints["1"]
	if len(hints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(hints))
	}
	if hints[0].Type != "CURRENCY" || hints[0].Value != "USD" {
		t.Errorf("hint = %+v, want Type=CURRENCY Value=USD", hints[0])
	}
}

func TestPlugin_ExtractBatch_NormalizesCurrencyCode(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []descpkg.BatchItem{
		{ID: "1", Hints: identifier.Hints{Currency: "  usd  ", SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ExtractBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if res.Hints["1"][0].Value != "USD" {
		t.Errorf("Value = %q, want USD", res.Hints["1"][0].Value)
	}
}

func TestPlugin_ExtractBatch_EmptyCurrency_ReturnsNothing(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []descpkg.BatchItem{
		{ID: "1", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintCash}},
	}
	res, err := p.ExtractBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if len(res.Hints) > 0 {
		t.Errorf("expected no hints when Currency empty, got %+v", res.Hints)
	}
	if res.Telemetry.Outcome != descpkg.OutcomeNoHints {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, descpkg.OutcomeNoHints)
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
