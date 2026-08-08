package api

import (
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
)

func TestExportCorporateEvents_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportCorporateEventStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestExportCorporateEvents_Empty(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListCorporateEventCoverageForExport(gomock.Any()).Return(nil, nil)
	db.EXPECT().ListStockSplitsForExport(gomock.Any()).Return(nil, nil)
	db.EXPECT().ListCashDividendsForExport(gomock.Any()).Return(nil, nil)
	stream := &exportCorporateEventStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream); err != nil {
		t.Fatalf("ExportCorporateEvents: %v", err)
	}
	// The envelope goes out even when there is nothing to say.
	if len(stream.sent) != 1 {
		t.Fatalf("expected exactly the envelope, got %d messages", len(stream.sent))
	}
	env := stream.sent[0].GetEnvelope()
	if env == nil {
		t.Fatal("first message is not the envelope")
	}
	if env.GetFormatVersion() != 1 || env.GetKind() != archivev1.ArchiveKind_SYSTEM {
		t.Fatalf("got format_version=%d kind=%v", env.GetFormatVersion(), env.GetKind())
	}
	if !env.GetExportedAt().IsValid() {
		t.Fatal("envelope carries no exported_at")
	}
}

// Knowledge time and dividend type must reach the wire: without them a
// PortfolioDB-to-PortfolioDB round trip restamps every split with the import
// time and turns special cash dividends into regular ones.
func TestExportCorporateEvents_CarriesKnowledgeTimeAndDividendType(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	splitKnownAt := time.Date(2015, 3, 4, 9, 30, 0, 0, time.UTC)
	divKnownAt := time.Date(2024, 2, 2, 8, 0, 0, 0, time.UTC)
	payDate := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)

	db.EXPECT().ListCorporateEventCoverageForExport(gomock.Any()).Return([]dbpkg.ExportCoverageRow{{
		IdentifierType:   "MIC_TICKER",
		IdentifierValue:  "AAPL",
		IdentifierDomain: "XNAS",
		AssetClass:       "STOCK",
		From:             time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Before:           time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}}, nil)
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
			PayDate:          &payDate,
			Amount:           "0.24",
			Currency:         "USD",
			Frequency:        "quarterly",
			Type:             "SC",
			FirstKnownAt:     divKnownAt,
		},
	}, nil)

	stream := &exportCorporateEventStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream); err != nil {
		t.Fatalf("ExportCorporateEvents: %v", err)
	}
	groups := stream.groups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.GetInstrument().GetType() != typev1.IdentifierType_MIC_TICKER ||
		g.GetInstrument().GetValue() != "AAPL" || g.GetInstrument().GetDomain() != "XNAS" {
		t.Fatalf("instrument ref = %v", g.GetInstrument())
	}
	if g.GetAssetClass() != typev1.AssetClass_STOCK {
		t.Fatalf("asset_class = %v", g.GetAssetClass())
	}
	if len(g.GetCoverage()) != 1 {
		t.Fatalf("expected 1 coverage span, got %d", len(g.GetCoverage()))
	}
	if cov := g.GetCoverage()[0]; cov.GetFrom() != "2020-01-01" || cov.GetBefore() != "2025-01-01" {
		t.Fatalf("got span [%s, %s)", cov.GetFrom(), cov.GetBefore())
	}
	if len(g.GetEvents()) != 2 {
		t.Fatalf("expected 2 events, got %d", len(g.GetEvents()))
	}

	split := g.GetEvents()[0].GetSplit()
	if split == nil {
		t.Fatalf("first event is not a split: %+v", g.GetEvents()[0])
	}
	if split.GetSplitFrom() != "1" || split.GetSplitTo() != "4" || split.GetExDate() != "2020-08-31" {
		t.Errorf("split = %v", split)
	}
	if got := split.GetFirstKnownAt().AsTime(); !got.Equal(splitKnownAt) {
		t.Errorf("split first_known_at: want %s, got %s", splitKnownAt, got)
	}

	div := g.GetEvents()[1].GetDividend()
	if div == nil {
		t.Fatalf("second event is not a dividend: %+v", g.GetEvents()[1])
	}
	if div.GetType() != archivev1.DividendType_SC {
		t.Errorf("dividend type: want SC, got %v", div.GetType())
	}
	if div.GetAmount() != "0.24" || div.GetCurrency() != "USD" || div.GetFrequency() != "quarterly" {
		t.Errorf("dividend = %v", div)
	}
	if div.GetPayDate() != "2024-02-15" || div.RecordDate != nil || div.DeclarationDate != nil {
		t.Errorf("dividend dates: pay=%q record=%v declaration=%v", div.GetPayDate(), div.RecordDate, div.DeclarationDate)
	}
	if got := div.GetFirstKnownAt().AsTime(); !got.Equal(divKnownAt) {
		t.Errorf("dividend first_known_at: want %s, got %s", divKnownAt, got)
	}
}

// A group with no events is the only way a file can say a provider was asked
// about those dates and had nothing, so it has to survive the grouping.
func TestExportCorporateEvents_CoveredInstrumentWithNoEvents(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListCorporateEventCoverageForExport(gomock.Any()).Return([]dbpkg.ExportCoverageRow{{
		IdentifierType:  "ISIN",
		IdentifierValue: "US0378331005",
		AssetClass:      "ETF",
		From:            time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Before:          time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
	}}, nil)
	db.EXPECT().ListStockSplitsForExport(gomock.Any()).Return(nil, nil)
	db.EXPECT().ListCashDividendsForExport(gomock.Any()).Return(nil, nil)

	stream := &exportCorporateEventStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream); err != nil {
		t.Fatalf("ExportCorporateEvents: %v", err)
	}
	groups := stream.groups()
	if len(groups) != 1 || len(groups[0].GetEvents()) != 0 {
		t.Fatalf("expected one group with no events, got %v", groups)
	}
	// The asset class of such a group can only come from the coverage row.
	if groups[0].GetAssetClass() != typev1.AssetClass_ETF {
		t.Fatalf("asset_class = %v", groups[0].GetAssetClass())
	}
}

// Two venues listing the same ticker are two instruments, so they are two
// groups: the domain is part of the key, not decoration on it.
func TestExportCorporateEvents_SplitsGroupsOnDomain(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().ListCorporateEventCoverageForExport(gomock.Any()).Return(nil, nil)
	db.EXPECT().ListStockSplitsForExport(gomock.Any()).Return([]dbpkg.ExportStockSplit{
		{IdentifierType: "MIC_TICKER", IdentifierValue: "VOD", IdentifierDomain: "XLON", AssetClass: "STOCK",
			ExDate: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), SplitFrom: "1", SplitTo: "2"},
		{IdentifierType: "MIC_TICKER", IdentifierValue: "VOD", IdentifierDomain: "XNAS", AssetClass: "STOCK",
			ExDate: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), SplitFrom: "1", SplitTo: "3"},
	}, nil)
	db.EXPECT().ListCashDividendsForExport(gomock.Any()).Return(nil, nil)

	stream := &exportCorporateEventStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportCorporateEvents(&apiv1.ExportCorporateEventsRequest{}, stream); err != nil {
		t.Fatalf("ExportCorporateEvents: %v", err)
	}
	groups := stream.groups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].GetInstrument().GetDomain() != "XLON" || groups[1].GetInstrument().GetDomain() != "XNAS" {
		t.Fatalf("domains = %q, %q", groups[0].GetInstrument().GetDomain(), groups[1].GetInstrument().GetDomain())
	}
}
