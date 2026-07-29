package api

import (
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestExportCorporateEvents_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportCorporateEventStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

// Knowledge time and dividend type must reach the wire: without them a
// PortfolioDB-to-PortfolioDB round trip restamps every split with the import
// time and turns special cash dividends into regular ones.
func TestExportCorporateEvents_CarriesKnowledgeTimeAndDividendType(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	splitKnownAt := time.Date(2015, 3, 4, 9, 30, 0, 0, time.UTC)
	divKnownAt := time.Date(2024, 2, 2, 8, 0, 0, 0, time.UTC)

	db.EXPECT().ListStockSplitsForExport(gomock.Any()).Return([]dbpkg.ExportStockSplit{
		{
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "XNAS",
			AssetClass:       "STOCK",
			DataProvider:     "massive",
			ExDate:           time.Date(2020, 8, 31, 0, 0, 0, 0, time.UTC),
			SplitFrom:        "1",
			SplitTo:          "4",
			FirstKnownAt:     splitKnownAt,
		},
	}, nil)
	db.EXPECT().ListCashDividendsForExport(gomock.Any()).Return([]dbpkg.ExportCashDividend{
		{
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "XNAS",
			AssetClass:       "STOCK",
			DataProvider:     "massive",
			ExDate:           time.Date(2024, 2, 9, 0, 0, 0, 0, time.UTC),
			Amount:           "0.24",
			Currency:         "USD",
			Type:             "SC",
			FirstKnownAt:     divKnownAt,
		},
	}, nil)

	stream := &exportCorporateEventStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream); err != nil {
		t.Fatalf("ExportCorporateEvents: %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("expected 2 rows streamed, got %d", len(stream.sent))
	}

	split := stream.sent[0].GetSplit()
	if split == nil {
		t.Fatalf("first row is not a split: %+v", stream.sent[0])
	}
	if got := split.GetFirstKnownAt().AsTime(); !got.Equal(splitKnownAt) {
		t.Errorf("split first_known_at: want %s, got %s", splitKnownAt, got)
	}

	div := stream.sent[1].GetDividend()
	if div == nil {
		t.Fatalf("second row is not a dividend: %+v", stream.sent[1])
	}
	if div.GetType() != "SC" {
		t.Errorf("dividend type: want SC, got %q", div.GetType())
	}
	if got := div.GetFirstKnownAt().AsTime(); !got.Equal(divKnownAt) {
		t.Errorf("dividend first_known_at: want %s, got %s", divKnownAt, got)
	}
}
