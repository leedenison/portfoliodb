package api

import (
	"errors"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// A block names an instrument by id, which means nothing to whoever has to decide
// whether to clear it, so the list resolves a name for each one.
func TestListPriceFetchBlocks(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	blocked := time.Date(2025, 5, 4, 8, 0, 0, 0, time.UTC)
	name := "APPLE INC"
	mockDB.EXPECT().ListPriceFetchBlocks(gomock.Any()).Return([]db.PriceFetchBlock{
		{InstrumentID: "i1", PluginID: "eodhd", Reason: "not found", FirstBlockedAt: blocked},
	}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{"i1"}).Return([]*db.InstrumentRow{
		{ID: "i1", Name: &name},
	}, nil)

	resp, err := srv.ListPriceFetchBlocks(adminCtx("admin-1", "sub|admin"), &apiv1.ListPriceFetchBlocksRequest{})
	if err != nil {
		t.Fatalf("ListPriceFetchBlocks: %v", err)
	}
	if len(resp.GetBlocks()) != 1 {
		t.Fatalf("blocks: got %d, want 1", len(resp.GetBlocks()))
	}
	b := resp.GetBlocks()[0]
	if b.GetInstrumentId() != "i1" || b.GetPluginId() != "eodhd" || b.GetReason() != "not found" {
		t.Errorf("block: got %+v", b)
	}
	if b.GetInstrumentDisplayName() != name {
		t.Errorf("instrument name: got %q, want %q", b.GetInstrumentDisplayName(), name)
	}
	if !b.GetFirstBlockedAt().AsTime().Equal(blocked) {
		t.Errorf("first blocked at: got %v, want %v", b.GetFirstBlockedAt().AsTime(), blocked)
	}
}

// An instrument with no name of its own falls back to its id, so a block is always
// identifiable even where resolution never got far enough to name it.
func TestListPriceFetchBlocks_UnnamedInstrumentFallsBackToItsID(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListPriceFetchBlocks(gomock.Any()).Return([]db.PriceFetchBlock{
		{InstrumentID: "i1", PluginID: "eodhd"},
	}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{"i1"}).Return(nil, nil)

	resp, err := srv.ListPriceFetchBlocks(adminCtx("admin-1", "sub|admin"), &apiv1.ListPriceFetchBlocksRequest{})
	if err != nil {
		t.Fatalf("ListPriceFetchBlocks: %v", err)
	}
	if got := resp.GetBlocks()[0].GetInstrumentDisplayName(); got != "i1" {
		t.Errorf("instrument name: got %q, want the id", got)
	}
}

// Nothing blocked is an empty list, and the instrument lookup is not made at all --
// there is nothing to look up, and asking for none would be a query per empty page.
func TestListPriceFetchBlocks_Empty(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListPriceFetchBlocks(gomock.Any()).Return(nil, nil)

	resp, err := srv.ListPriceFetchBlocks(adminCtx("admin-1", "sub|admin"), &apiv1.ListPriceFetchBlocksRequest{})
	if err != nil {
		t.Fatalf("ListPriceFetchBlocks: %v", err)
	}
	if len(resp.GetBlocks()) != 0 {
		t.Errorf("blocks: got %d, want none", len(resp.GetBlocks()))
	}
}

func TestListPriceFetchBlocks_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListPriceFetchBlocks(gomock.Any()).Return(nil, errors.New("boom"))

	_, err := srv.ListPriceFetchBlocks(adminCtx("admin-1", "sub|admin"), &apiv1.ListPriceFetchBlocksRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestDeletePriceFetchBlock(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().DeletePriceFetchBlock(gomock.Any(), "i1", "eodhd").Return(nil)

	_, err := srv.DeletePriceFetchBlock(adminCtx("admin-1", "sub|admin"), &apiv1.DeletePriceFetchBlockRequest{
		InstrumentId: "i1", PluginId: "eodhd",
	})
	if err != nil {
		t.Fatalf("DeletePriceFetchBlock: %v", err)
	}
}

// A block is one pair, so half of one names a set rather than a row.
func TestDeletePriceFetchBlock_NeedsBothHalvesOfThePair(t *testing.T) {
	tests := []struct {
		name string
		req  *apiv1.DeletePriceFetchBlockRequest
	}{
		{"no instrument", &apiv1.DeletePriceFetchBlockRequest{PluginId: "eodhd"}},
		{"no plugin", &apiv1.DeletePriceFetchBlockRequest{InstrumentId: "i1"}},
		{"neither", &apiv1.DeletePriceFetchBlockRequest{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newAPIServerWithMock(t)
			_, err := srv.DeletePriceFetchBlock(adminCtx("admin-1", "sub|admin"), tt.req)
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestPriceFetchBlocks_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	t.Run("ListPriceFetchBlocks", func(t *testing.T) {
		_, err := srv.ListPriceFetchBlocks(ctx, &apiv1.ListPriceFetchBlocksRequest{})
		testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
	})
	t.Run("DeletePriceFetchBlock", func(t *testing.T) {
		_, err := srv.DeletePriceFetchBlock(ctx, &apiv1.DeletePriceFetchBlockRequest{InstrumentId: "i1", PluginId: "eodhd"})
		testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
	})
}
