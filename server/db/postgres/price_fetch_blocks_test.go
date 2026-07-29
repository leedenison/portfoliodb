package postgres

import (
	"context"
	"testing"
)

func TestCreatePriceFetchBlock_PreservesFirstBlockedAt(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.CreatePriceFetchBlock(ctx, instID, "massive", "404"); err != nil {
		t.Fatalf("create: %v", err)
	}
	past := backdate(t, p,
		`UPDATE price_fetch_blocks SET first_blocked_at = $1 WHERE instrument_id = $2`, instID)

	// Blocking again records a newer reason, not a newer first-blocked-at.
	if err := p.CreatePriceFetchBlock(ctx, instID, "massive", "subscription limit"); err != nil {
		t.Fatalf("re-create: %v", err)
	}

	blocks, err := p.ListPriceFetchBlocks(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Reason != "subscription limit" {
		t.Errorf("reason not updated: %q", blocks[0].Reason)
	}
	if !blocks[0].FirstBlockedAt.Equal(past) {
		t.Errorf("first blocked at moved on re-block: want %s, got %s", past, blocks[0].FirstBlockedAt)
	}
}
