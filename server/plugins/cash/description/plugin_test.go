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
		{ID: "1", InstrumentDescription: "USD Cash", Hints: identifier.Hints{Currency: "USD", SecurityType: "Cash"}},
	}
	out, err := p.ExtractBatch(ctx, nil, "IBKR", "IBKR:test", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil map")
	}
	hints := out["1"]
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
		{ID: "1", Hints: identifier.Hints{Currency: "  usd  ", SecurityType: "Cash"}},
	}
	out, err := p.ExtractBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if out["1"][0].Value != "USD" {
		t.Errorf("Value = %q, want USD", out["1"][0].Value)
	}
}

func TestPlugin_ExtractBatch_EmptyCurrency_ReturnsNothing(t *testing.T) {
	p := NewPlugin()
	ctx := context.Background()
	items := []descpkg.BatchItem{
		{ID: "1", Hints: identifier.Hints{SecurityType: "Cash"}},
	}
	out, err := p.ExtractBatch(ctx, nil, "", "", items)
	if err != nil {
		t.Fatalf("ExtractBatch: %v", err)
	}
	if out != nil && len(out) > 0 {
		t.Errorf("expected nil or empty map when Currency empty, got %+v", out)
	}
}

func TestPlugin_AcceptableSecurityTypes_OnlyCash(t *testing.T) {
	p := NewPlugin()
	types := p.AcceptableSecurityTypes()
	if len(types) != 1 || types[0] != "Cash" {
		t.Errorf("AcceptableSecurityTypes = %v, want [Cash]", types)
	}
}
