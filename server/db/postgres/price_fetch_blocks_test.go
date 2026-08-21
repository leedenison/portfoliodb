package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
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

// The export names an instrument by identifier rather than by id, and reads
// both tables through the same query.
func TestListPriceFetchBlocksForExport(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.CreatePriceFetchBlock(ctx, instID, "massive", "404"); err != nil {
		t.Fatalf("create: %v", err)
	}

	blocks, err := p.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Ref.Type != "BROKER_DESCRIPTION" || blocks[0].Ref.Value != "AAPL" {
		t.Fatalf("identifier = %s %s", blocks[0].Ref.Type, blocks[0].Ref.Value)
	}
	if blocks[0].PluginID != "massive" || blocks[0].Reason != "404" {
		t.Fatalf("block = %+v", blocks[0])
	}
	if blocks[0].FirstBlockedAt.IsZero() {
		t.Fatal("first_blocked_at not carried")
	}
}

// first_blocked_at records when the pair was first blocked and is never
// overwritten, so an import can move it backwards and must not move it
// forwards.
func TestUpsertPriceFetchBlocks_KeepsTheEarlierFirstBlockedAt(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	stored := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertPriceFetchBlocks(ctx, []db.FetchBlockInput{
		{InstrumentID: instID, PluginID: "massive", Reason: "404", FirstBlockedAt: stored},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A later file does not restate the pair as newly blocked.
	later := stored.AddDate(0, 1, 0)
	if err := p.UpsertPriceFetchBlocks(ctx, []db.FetchBlockInput{
		{InstrumentID: instID, PluginID: "massive", Reason: "subscription limit", FirstBlockedAt: later},
	}); err != nil {
		t.Fatalf("upsert later: %v", err)
	}
	blocks, err := p.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !blocks[0].FirstBlockedAt.UTC().Equal(stored) {
		t.Fatalf("first_blocked_at = %v, want the earlier %v", blocks[0].FirstBlockedAt.UTC(), stored)
	}
	if blocks[0].Reason != "subscription limit" {
		t.Fatalf("reason = %q, want the file's", blocks[0].Reason)
	}

	// An earlier file does move it back: the pair was blocked before this
	// instance knew about it.
	earlier := stored.AddDate(0, -1, 0)
	if err := p.UpsertPriceFetchBlocks(ctx, []db.FetchBlockInput{
		{InstrumentID: instID, PluginID: "massive", Reason: "404", FirstBlockedAt: earlier},
	}); err != nil {
		t.Fatalf("upsert earlier: %v", err)
	}
	blocks, err = p.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !blocks[0].FirstBlockedAt.UTC().Equal(earlier) {
		t.Fatalf("first_blocked_at = %v, want %v", blocks[0].FirstBlockedAt.UTC(), earlier)
	}
}

// The corporate event table is the same shape and shares the query, so it gets
// the round trip rather than the whole set of cases again.
func TestCorporateEventFetchBlocks_ExportRoundTrip(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	blocked := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if err := p.UpsertCorporateEventFetchBlocks(ctx, []db.FetchBlockInput{
		{InstrumentID: instID, PluginID: "massive", Reason: "no events endpoint", FirstBlockedAt: blocked},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	blocks, err := p.ListCorporateEventFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Reason != "no events endpoint" {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !blocks[0].FirstBlockedAt.UTC().Equal(blocked) {
		t.Fatalf("first_blocked_at = %v", blocks[0].FirstBlockedAt.UTC())
	}
	// And the price table is untouched by it.
	priceBlocks, err := p.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list price: %v", err)
	}
	if len(priceBlocks) != 0 {
		t.Fatalf("price blocks = %+v", priceBlocks)
	}
}
