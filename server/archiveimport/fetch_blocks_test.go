package archiveimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

func fetchBlockPart(groups ...*archivev1.FetchBlockGroup) *archivev1.FetchBlockPart {
	return &archivev1.FetchBlockPart{Groups: groups}
}

func fetchBlockGroup(value string, blocks ...*archivev1.FetchBlock) *archivev1.FetchBlockGroup {
	return &archivev1.FetchBlockGroup{
		Instrument: &archivev1.InstrumentRef{
			Type:   typev1.IdentifierType_MIC_TICKER,
			Value:  value,
			Domain: "XNAS",
		},
		Blocks: blocks,
	}
}

// expectFound makes the identifier lookup succeed for every group.
func expectFound(database *mock.MockDB, id string) {
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(id, nil).AnyTimes()
}

func TestFetchBlockPart_SplitsByCategory(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	blocked := time.Date(2026, 3, 4, 9, 12, 0, 0, time.UTC)

	database.EXPECT().UpsertPriceFetchBlocks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, blocks []db.FetchBlockInput) error {
			if len(blocks) != 1 || blocks[0].PluginID != "eodhd" {
				t.Errorf("price blocks = %+v", blocks)
			}
			if !blocks[0].FirstBlockedAt.Equal(blocked) {
				t.Errorf("first_blocked_at = %v", blocks[0].FirstBlockedAt)
			}
			return nil
		})
	database.EXPECT().UpsertCorporateEventFetchBlocks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, blocks []db.FetchBlockInput) error {
			if len(blocks) != 1 || blocks[0].PluginID != "massive" {
				t.Errorf("event blocks = %+v", blocks)
			}
			return nil
		})

	part := fetchBlockPart(fetchBlockGroup("AAPL",
		&archivev1.FetchBlock{
			Category: typev1.PluginCategory_PRICE, PluginId: "eodhd", Reason: "404",
			FirstBlockedAt: timestamppb.New(blocked),
		},
		&archivev1.FetchBlock{
			Category: typev1.PluginCategory_CORPORATE_EVENT, PluginId: "massive", Reason: "403",
			FirstBlockedAt: timestamppb.New(blocked),
		},
	))
	written, err := FetchBlockPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("FetchBlockPart: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
}

// A block whose file does not say when it was first blocked falls back to the
// envelope's knowledge time rather than to now.
func TestFetchBlockPart_FallsBackToTheEnvelope(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)

	database.EXPECT().UpsertPriceFetchBlocks(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, blocks []db.FetchBlockInput) error {
			if !blocks[0].FirstBlockedAt.Equal(asOf) {
				t.Errorf("first_blocked_at = %v, want %v", blocks[0].FirstBlockedAt, asOf)
			}
			return nil
		})
	database.EXPECT().UpsertCorporateEventFetchBlocks(gomock.Any(), gomock.Any()).Return(nil)

	part := fetchBlockPart(fetchBlockGroup("AAPL",
		&archivev1.FetchBlock{Category: typev1.PluginCategory_PRICE, PluginId: "eodhd", Reason: "404"}))
	if _, err := FetchBlockPart(context.Background(), database, part, &asOf, rep); err != nil {
		t.Fatalf("FetchBlockPart: %v", err)
	}
}

// An instrument the instance does not have is a rejected group. A block says a
// plugin should not be called about an instrument, so paying a plugin to invent
// that instrument would be self-defeating.
func TestFetchBlockPart_UnknownInstrumentIsRejectedNotResolved(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("", nil)
	database.EXPECT().UpsertPriceFetchBlocks(gomock.Any(), gomock.Len(0)).Return(nil)
	database.EXPECT().UpsertCorporateEventFetchBlocks(gomock.Any(), gomock.Len(0)).Return(nil)

	part := fetchBlockPart(fetchBlockGroup("AAPL",
		&archivev1.FetchBlock{Category: typev1.PluginCategory_PRICE, PluginId: "eodhd", Reason: "404"}))
	written, err := FetchBlockPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("FetchBlockPart: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "instrument" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

// A category with no fetch block table describes a table that does not exist.
// The block is rejected and the group's other blocks still land.
func TestFetchBlockPart_CategoryWithNoTableIsRejected(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	database.EXPECT().UpsertPriceFetchBlocks(gomock.Any(), gomock.Len(1)).Return(nil)
	database.EXPECT().UpsertCorporateEventFetchBlocks(gomock.Any(), gomock.Len(0)).Return(nil)

	part := fetchBlockPart(fetchBlockGroup("AAPL",
		&archivev1.FetchBlock{Category: typev1.PluginCategory_IDENTIFIER, PluginId: "openfigi", Reason: "404"},
		&archivev1.FetchBlock{Category: typev1.PluginCategory_PRICE, PluginId: "eodhd", Reason: "404"},
	))
	written, err := FetchBlockPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("FetchBlockPart: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "category" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

// A write that does not land fails the part, unlike a rejected row.
func TestFetchBlockPart_WriteFailureFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	expectFound(database, "inst-1")
	database.EXPECT().UpsertPriceFetchBlocks(gomock.Any(), gomock.Any()).Return(errors.New("boom"))

	part := fetchBlockPart(fetchBlockGroup("AAPL",
		&archivev1.FetchBlock{Category: typev1.PluginCategory_PRICE, PluginId: "eodhd", Reason: "404"}))
	if _, err := FetchBlockPart(context.Background(), database, part, nil, rep); err == nil {
		t.Fatal("expected the part to fail")
	}
}

func TestFetchBlockPart_EmptyPart(t *testing.T) {
	database, rep := newPartTest(t)
	written, err := FetchBlockPart(context.Background(), database, fetchBlockPart(), nil, rep)
	if err != nil || written != 0 {
		t.Fatalf("FetchBlockPart = %d, %v", written, err)
	}
}
