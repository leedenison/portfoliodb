package api

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
)

func exportBlock(value, plugin, reason string, blockedAt time.Time) dbpkg.ExportFetchBlock {
	return dbpkg.ExportFetchBlock{
		IdentifierType:   "MIC_TICKER",
		IdentifierValue:  value,
		IdentifierDomain: "XNAS",
		PluginID:         plugin,
		Reason:           reason,
		FirstBlockedAt:   blockedAt,
	}
}

func exportFetchBlocks(t *testing.T, price, events []dbpkg.ExportFetchBlock) []*archivev1.FetchBlockGroup {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListPriceFetchBlocksForExport(gomock.Any()).Return(price, nil)
	mockDB.EXPECT().ListCorporateEventFetchBlocksForExport(gomock.Any()).Return(events, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_FETCH_BLOCKS},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	return stream.fetchBlockGroups()
}

// The two tables travel in one part, so an instrument blocked in both appears
// once with two blocks rather than twice with one each.
func TestExportSystemArchive_FetchBlocks_MergesBothFetchers(t *testing.T) {
	blocked := time.Date(2026, 3, 4, 9, 12, 0, 0, time.UTC)
	groups := exportFetchBlocks(t,
		[]dbpkg.ExportFetchBlock{exportBlock("AAPL", "eodhd", "404 from provider", blocked)},
		[]dbpkg.ExportFetchBlock{exportBlock("AAPL", "massive", "403 from provider", blocked)},
	)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	blocks := groups[0].GetBlocks()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].GetCategory() != typev1.PluginCategory_PRICE {
		t.Fatalf("first block category = %s", blocks[0].GetCategory())
	}
	if blocks[1].GetCategory() != typev1.PluginCategory_CORPORATE_EVENT {
		t.Fatalf("second block category = %s", blocks[1].GetCategory())
	}
	if blocks[0].GetReason() != "404 from provider" {
		t.Fatalf("reason = %q", blocks[0].GetReason())
	}
	if !blocks[0].GetFirstBlockedAt().AsTime().Equal(blocked) {
		t.Fatalf("first_blocked_at = %v, want %v", blocks[0].GetFirstBlockedAt().AsTime(), blocked)
	}
	if groups[0].GetInstrument().GetType() != typev1.IdentifierType_MIC_TICKER {
		t.Fatalf("instrument = %v", groups[0].GetInstrument())
	}
}

// An instrument blocked for only one fetcher still gets its own group.
func TestExportSystemArchive_FetchBlocks_SeparateInstrumentsSeparateGroups(t *testing.T) {
	blocked := time.Date(2026, 3, 4, 9, 12, 0, 0, time.UTC)
	groups := exportFetchBlocks(t,
		[]dbpkg.ExportFetchBlock{exportBlock("AAPL", "eodhd", "404", blocked)},
		[]dbpkg.ExportFetchBlock{exportBlock("MSFT", "massive", "403", blocked)},
	)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].GetInstrument().GetValue() != "AAPL" || groups[1].GetInstrument().GetValue() != "MSFT" {
		t.Fatalf("groups = %s, %s", groups[0].GetInstrument().GetValue(), groups[1].GetInstrument().GetValue())
	}
}

// The same ticker on two exchanges is two instruments, so the domain is part of
// the key rather than something the merge can drop.
func TestExportSystemArchive_FetchBlocks_DomainSplitsGroups(t *testing.T) {
	blocked := time.Date(2026, 3, 4, 9, 12, 0, 0, time.UTC)
	other := exportBlock("BHP", "eodhd", "404", blocked)
	other.IdentifierDomain = "XLON"
	groups := exportFetchBlocks(t,
		[]dbpkg.ExportFetchBlock{exportBlock("BHP", "eodhd", "404", blocked), other},
		nil,
	)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}
