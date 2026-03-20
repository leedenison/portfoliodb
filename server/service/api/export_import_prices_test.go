package api

import (
	"context"
	"testing"
	"time"

	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestExportPrices_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportPriceStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestExportPrices_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	open := 185.5
	vol := int64(50000000)
	rows := []dbpkg.ExportPriceRow{
		{
			IdentifierType:   "ISIN",
			IdentifierValue:  "US0378331005",
			IdentifierDomain: "",
			PriceDate:        time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Open:             &open,
			Close:            185.90,
			Volume:           &vol,
		},
	}
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return(rows, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	if err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 row streamed, got %d", len(stream.sent))
	}
	row := stream.sent[0]
	if row.GetIdentifierType() != "ISIN" || row.GetIdentifierValue() != "US0378331005" {
		t.Fatalf("got identifier %s %s", row.GetIdentifierType(), row.GetIdentifierValue())
	}
	if row.GetPriceDate() != "2024-01-15" {
		t.Fatalf("expected date 2024-01-15, got %s", row.GetPriceDate())
	}
	if row.GetClose() != 185.90 {
		t.Fatalf("expected close=185.90, got %v", row.GetClose())
	}
	if row.Open == nil || *row.Open != 185.5 {
		t.Fatalf("expected open=185.5, got %v", row.Open)
	}
	if row.Volume == nil || *row.Volume != 50000000 {
		t.Fatalf("expected volume=50000000, got %v", row.Volume)
	}
}

func TestExportPrices_Empty(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPricesForExport(gomock.Any()).
		Return(nil, nil)
	stream := &exportPriceStreamMock{ctx: adminCtx("user-1", "sub|1")}
	err := srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
	if err != nil {
		t.Fatalf("ExportPrices: %v", err)
	}
	if len(stream.sent) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(stream.sent))
	}
}

func TestImportPrices_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType: "ISIN", IdentifierValue: "US0378331005",
			PriceDate: "2024-01-15", Close: 100,
		}},
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportPrices_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").
		Return("inst-1", nil)
	db.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, prices []dbpkg.EODPrice) error {
			if len(prices) != 1 {
				t.Errorf("expected 1 price, got %d", len(prices))
			}
			if prices[0].InstrumentID != "inst-1" {
				t.Errorf("expected inst-1, got %s", prices[0].InstrumentID)
			}
			if prices[0].DataProvider != "import" {
				t.Errorf("expected data_provider=import, got %s", prices[0].DataProvider)
			}
			if prices[0].Close != 185.90 {
				t.Errorf("expected close=185.90, got %v", prices[0].Close)
			}
			return nil
		})
	ctx := adminCtx("user-1", "sub|1")
	open := 185.5
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:  "ISIN",
			IdentifierValue: "US0378331005",
			PriceDate:       "2024-01-15",
			Open:            &open,
			Close:           185.90,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 1 || len(resp.GetErrors()) != 0 {
		t.Fatalf("expected upserted=1, errors=0; got upserted=%d, errors=%d", resp.GetUpsertedCount(), len(resp.GetErrors()))
	}
}

func TestImportPrices_UnknownInstrument(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "UNKNOWN123").
		Return("", nil)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:  "ISIN",
			IdentifierValue: "UNKNOWN123",
			PriceDate:       "2024-01-15",
			Close:           100,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("expected upserted=0, errors=1; got upserted=%d, errors=%d", resp.GetUpsertedCount(), len(resp.GetErrors()))
	}
	if resp.GetErrors()[0].GetIndex() != 0 {
		t.Fatalf("expected error index=0, got %d", resp.GetErrors()[0].GetIndex())
	}
}

func TestImportPrices_InvalidDate(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:  "ISIN",
			IdentifierValue: "US0378331005",
			PriceDate:       "not-a-date",
			Close:           100,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("expected upserted=0, errors=1; got upserted=%d, errors=%d", resp.GetUpsertedCount(), len(resp.GetErrors()))
	}
}

func TestImportPrices_BrokerDescription(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "Fidelity:web:fidelity-csv", "APPLE INC").
		Return("inst-2", nil)
	db.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:   "BROKER_DESCRIPTION",
			IdentifierValue:  "APPLE INC",
			IdentifierDomain: "Fidelity:web:fidelity-csv",
			PriceDate:        "2024-01-15",
			Close:            185.90,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 1 {
		t.Fatalf("expected upserted=1, got %d", resp.GetUpsertedCount())
	}
}

func TestImportPrices_TickerWithDomain(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "XNAS", "AAPL").
		Return("inst-3", nil)
	db.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:   "TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "XNAS",
			PriceDate:        "2024-01-15",
			Close:            185.90,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 1 {
		t.Fatalf("expected upserted=1, got %d", resp.GetUpsertedCount())
	}
}

func TestImportPrices_Empty(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetUpsertedCount() != 0 || len(resp.GetErrors()) != 0 {
		t.Fatalf("expected empty response, got upserted=%d, errors=%d", resp.GetUpsertedCount(), len(resp.GetErrors()))
	}
}
