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

func TestListPriceGaps_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListPriceGaps(ctx, &apiv1.ListPriceGapsRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestListPriceGaps_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListPriceGaps(ctxNoAuth(), &apiv1.ListPriceGapsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestListPriceGaps_Empty(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	db.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	db.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)

	resp, err := srv.ListPriceGaps(adminCtx("user-1", "sub|1"), &apiv1.ListPriceGapsRequest{})
	if err != nil {
		t.Fatalf("ListPriceGaps: %v", err)
	}
	if len(resp.GetPriceGaps()) != 0 {
		t.Fatalf("expected 0 price gaps, got %d", len(resp.GetPriceGaps()))
	}
	if len(resp.GetFxGaps()) != 0 {
		t.Fatalf("expected 0 fx gaps, got %d", len(resp.GetFxGaps()))
	}
}

func TestListPriceGaps_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)

	stockAC := "STOCK"
	mic := "XNAS"
	currency := "USD"
	name := "Apple Inc"
	priceGaps := []dbpkg.InstrumentDateRanges{
		{
			InstrumentID: "inst-1",
			Ranges: []dbpkg.DateRange{
				{From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
	}
	fxGaps := []dbpkg.InstrumentDateRanges{
		{
			InstrumentID: "inst-fx",
			Ranges: []dbpkg.DateRange{
				{From: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)},
			},
		},
	}

	fxAC := "FX"
	fxName := "GBPUSD"
	instruments := []*dbpkg.InstrumentRow{
		{
			ID: "inst-1", AssetClass: &stockAC, ExchangeMIC: &mic, Currency: &currency, Name: &name,
			Exchange: "NASDAQ",
			Identifiers: []dbpkg.IdentifierInput{
				{Type: "BROKER_DESCRIPTION", Domain: "src", Value: "AAPL", Canonical: false},
				{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true},
				{Type: "ISIN", Value: "US0378331005", Canonical: true},
			},
		},
		{
			ID: "inst-fx", AssetClass: &fxAC, Currency: &currency, Name: &fxName,
			Identifiers: []dbpkg.IdentifierInput{
				{Type: "FX_PAIR", Value: "GBPUSD", Canonical: true},
			},
		},
	}

	db.EXPECT().PriceGaps(gomock.Any(), dbpkg.HeldRangesOpts{ExtendToToday: true}).Return(priceGaps, nil)
	db.EXPECT().FXGaps(gomock.Any(), dbpkg.HeldRangesOpts{ExtendToToday: true}).Return(fxGaps, nil)
	db.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(instruments, nil)

	resp, err := srv.ListPriceGaps(adminCtx("user-1", "sub|1"), &apiv1.ListPriceGapsRequest{})
	if err != nil {
		t.Fatalf("ListPriceGaps: %v", err)
	}

	if len(resp.GetPriceGaps()) != 1 {
		t.Fatalf("expected 1 price gap, got %d", len(resp.GetPriceGaps()))
	}
	pg := resp.GetPriceGaps()[0]
	if pg.GetInstrumentId() != "inst-1" {
		t.Fatalf("expected instrument_id=inst-1, got %s", pg.GetInstrumentId())
	}
	if pg.GetIdentifier().GetType() != apiv1.IdentifierType_MIC_TICKER {
		t.Fatalf("expected MIC_TICKER identifier, got %s", pg.GetIdentifier().GetType())
	}
	if pg.GetIdentifier().GetValue() != "AAPL" {
		t.Fatalf("expected value=AAPL, got %s", pg.GetIdentifier().GetValue())
	}
	if pg.GetIdentifier().GetDomain() != "XNAS" {
		t.Fatalf("expected domain=XNAS, got %s", pg.GetIdentifier().GetDomain())
	}
	if pg.GetAssetClass() != apiv1.AssetClass_ASSET_CLASS_STOCK {
		t.Fatalf("expected STOCK, got %s", pg.GetAssetClass())
	}
	if pg.GetName() != "Apple Inc" {
		t.Fatalf("expected name=Apple Inc, got %s", pg.GetName())
	}
	if len(pg.GetGaps()) != 1 {
		t.Fatalf("expected 1 gap range, got %d", len(pg.GetGaps()))
	}
	if pg.GetGaps()[0].GetFrom() != "2024-01-01" || pg.GetGaps()[0].GetTo() != "2024-06-01" {
		t.Fatalf("unexpected gap range: %s - %s", pg.GetGaps()[0].GetFrom(), pg.GetGaps()[0].GetTo())
	}

	if len(resp.GetFxGaps()) != 1 {
		t.Fatalf("expected 1 fx gap, got %d", len(resp.GetFxGaps()))
	}
	fx := resp.GetFxGaps()[0]
	if fx.GetIdentifier().GetType() != apiv1.IdentifierType_FX_PAIR {
		t.Fatalf("expected FX_PAIR identifier, got %s", fx.GetIdentifier().GetType())
	}
	if fx.GetIdentifier().GetValue() != "GBPUSD" {
		t.Fatalf("expected value=GBPUSD, got %s", fx.GetIdentifier().GetValue())
	}
}

func TestListPriceGaps_AssetClassFilter(t *testing.T) {
	srv, db := newAPIServerWithMock(t)

	stockAC := "STOCK"
	etfAC := "ETF"
	instruments := []*dbpkg.InstrumentRow{
		{
			ID: "inst-stock", AssetClass: &stockAC, Exchange: "NASDAQ",
			Identifiers: []dbpkg.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true}},
		},
		{
			ID: "inst-etf", AssetClass: &etfAC, Exchange: "NYSE Arca",
			Identifiers: []dbpkg.IdentifierInput{{Type: "MIC_TICKER", Domain: "ARCX", Value: "SPY", Canonical: true}},
		},
	}
	priceGaps := []dbpkg.InstrumentDateRanges{
		{InstrumentID: "inst-stock", Ranges: []dbpkg.DateRange{{From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)}}},
		{InstrumentID: "inst-etf", Ranges: []dbpkg.DateRange{{From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)}}},
	}

	db.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(priceGaps, nil)
	db.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	db.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(instruments, nil)

	resp, err := srv.ListPriceGaps(adminCtx("user-1", "sub|1"), &apiv1.ListPriceGapsRequest{
		AssetClasses: []apiv1.AssetClass{apiv1.AssetClass_ASSET_CLASS_ETF},
	})
	if err != nil {
		t.Fatalf("ListPriceGaps: %v", err)
	}
	if len(resp.GetPriceGaps()) != 1 {
		t.Fatalf("expected 1 filtered gap, got %d", len(resp.GetPriceGaps()))
	}
	if resp.GetPriceGaps()[0].GetInstrumentId() != "inst-etf" {
		t.Fatalf("expected inst-etf, got %s", resp.GetPriceGaps()[0].GetInstrumentId())
	}
}

func TestListPriceGaps_SkipsInstrumentsWithoutUsableIdentifier(t *testing.T) {
	srv, db := newAPIServerWithMock(t)

	stockAC := "STOCK"
	instruments := []*dbpkg.InstrumentRow{
		{
			ID: "inst-no-id", AssetClass: &stockAC,
			Identifiers: []dbpkg.IdentifierInput{
				{Type: "BROKER_DESCRIPTION", Domain: "src", Value: "Some Stock", Canonical: false},
			},
		},
	}
	priceGaps := []dbpkg.InstrumentDateRanges{
		{InstrumentID: "inst-no-id", Ranges: []dbpkg.DateRange{{From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)}}},
	}

	db.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(priceGaps, nil)
	db.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	db.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(instruments, nil)

	resp, err := srv.ListPriceGaps(adminCtx("user-1", "sub|1"), &apiv1.ListPriceGapsRequest{})
	if err != nil {
		t.Fatalf("ListPriceGaps: %v", err)
	}
	if len(resp.GetPriceGaps()) != 0 {
		t.Fatalf("expected 0 gaps (no usable identifier), got %d", len(resp.GetPriceGaps()))
	}
}

func TestBestIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		ids      []dbpkg.IdentifierInput
		wantType string
		wantVal  string
	}{
		{
			name:     "prefers MIC_TICKER over ISIN",
			ids:      []dbpkg.IdentifierInput{{Type: "ISIN", Value: "US0378331005"}, {Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
			wantType: "MIC_TICKER",
			wantVal:  "AAPL",
		},
		{
			name:     "prefers OPENFIGI_TICKER over FX_PAIR",
			ids:      []dbpkg.IdentifierInput{{Type: "FX_PAIR", Value: "GBPUSD"}, {Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"}},
			wantType: "OPENFIGI_TICKER",
			wantVal:  "AAPL",
		},
		{
			name:     "falls back to FX_PAIR",
			ids:      []dbpkg.IdentifierInput{{Type: "FX_PAIR", Value: "EURUSD"}},
			wantType: "FX_PAIR",
			wantVal:  "EURUSD",
		},
		{
			name: "no usable identifier",
			ids:  []dbpkg.IdentifierInput{{Type: "BROKER_DESCRIPTION", Value: "foo"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bestIdentifier(tc.ids)
			if tc.wantType == "" {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil identifier")
			}
			if got.GetType().String() != tc.wantType {
				t.Fatalf("expected type=%s, got %s", tc.wantType, got.GetType().String())
			}
			if got.GetValue() != tc.wantVal {
				t.Fatalf("expected value=%s, got %s", tc.wantVal, got.GetValue())
			}
		})
	}
}
