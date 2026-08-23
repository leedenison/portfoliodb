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

	if err := p.CreatePriceFetchBlock(ctx, pricedListing(t, p, instID), "massive", "404"); err != nil {
		t.Fatalf("create: %v", err)
	}
	past := backdate(t, p,
		`UPDATE price_fetch_blocks SET first_blocked_at = $1 WHERE listing_id = $2`,
		pricedListing(t, p, instID))

	// Blocking again records a newer reason, not a newer first-blocked-at.
	if err := p.CreatePriceFetchBlock(ctx, pricedListing(t, p, instID), "massive", "subscription limit"); err != nil {
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

// The export names the security by identifier rather than by id, and says which
// of its lines by currency. Both tables read through the same query.
func TestListPriceFetchBlocksForExport(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "AAPL")

	if err := p.CreatePriceFetchBlock(ctx, pricedListing(t, p, instID), "massive", "404"); err != nil {
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
	if blocks[0].Currency != "USD" {
		t.Fatalf("currency = %q, want the blocked line's", blocks[0].Currency)
	}
	if blocks[0].PluginID != "massive" || blocks[0].Reason != "404" {
		t.Fatalf("block = %+v", blocks[0])
	}
	if blocks[0].FirstBlockedAt.IsZero() {
		t.Fatal("first_blocked_at not carried")
	}
}

// One security, two lines, one of them blocked. A block keyed on the security
// could not say which, and the export has to carry the answer or the file
// restores it onto whichever line the importer happened to pick.
func TestPriceFetchBlocks_BlockOneLineNotTheOther(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	instID := setupInstrument(t, p, "VOD")
	usd := pricedListing(t, p, instID)
	gbp, err := p.EnsureListing(ctx, instID, "GBP")
	if err != nil {
		t.Fatalf("ensure GBP listing: %v", err)
	}

	if err := p.CreatePriceFetchBlock(ctx, gbp, "massive", "404"); err != nil {
		t.Fatalf("create: %v", err)
	}

	blocked, err := p.BlockedPluginsForListings(ctx, []string{gbp, usd})
	if err != nil {
		t.Fatalf("blocked plugins: %v", err)
	}
	if !blocked[gbp]["massive"] {
		t.Error("the GBP line is not blocked")
	}
	if blocked[usd]["massive"] {
		t.Error("blocking the GBP line blocked the USD line as well")
	}

	blocks, err := p.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		t.Fatalf("list for export: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Currency != "GBP" {
		t.Fatalf("exported blocks = %+v, want one naming GBP", blocks)
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
		{OwnerID: pricedListing(t, p, instID), PluginID: "massive", Reason: "404", FirstBlockedAt: stored},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A later file does not restate the pair as newly blocked.
	later := stored.AddDate(0, 1, 0)
	if err := p.UpsertPriceFetchBlocks(ctx, []db.FetchBlockInput{
		{OwnerID: pricedListing(t, p, instID), PluginID: "massive", Reason: "subscription limit", FirstBlockedAt: later},
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
		{OwnerID: pricedListing(t, p, instID), PluginID: "massive", Reason: "404", FirstBlockedAt: earlier},
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
		{OwnerID: instID, PluginID: "massive", Reason: "no events endpoint", FirstBlockedAt: blocked},
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
