package api

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
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
			IdentifierType:   "MIC_TICKER",
			IdentifierValue:  "AAPL",
			IdentifierDomain: "US",
			AssetClass:       "STOCK",
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
	if row.GetIdentifierType() != "MIC_TICKER" || row.GetIdentifierValue() != "AAPL" {
		t.Fatalf("got identifier %s %s", row.GetIdentifierType(), row.GetIdentifierValue())
	}
	if row.GetAssetClass() != apiv1.AssetClass_ASSET_CLASS_STOCK {
		t.Fatalf("expected asset_class=ASSET_CLASS_STOCK, got %s", row.GetAssetClass())
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

func TestImportPrices_Empty_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestImportPrices_Success_CreatesJob(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	var enqueued bool
	srv.enqueueJob = func(jobID, jobType string) error {
		enqueued = true
		if jobType != "price" {
			t.Errorf("expected job type price, got %s", jobType)
		}
		return nil
	}
	db.EXPECT().
		CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, p dbpkg.CreateJobParams) (string, error) {
			if p.JobType != "price" {
				t.Errorf("expected job_type=price, got %s", p.JobType)
			}
			if p.UserID != "user-1" {
				t.Errorf("expected user_id=user-1, got %s", p.UserID)
			}
			if len(p.Payload) == 0 {
				t.Error("expected non-empty payload")
			}
			return "job-456", nil
		})
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{{
			IdentifierType:  "ISIN",
			IdentifierValue: "US0378331005",
			PriceDate:       "2024-01-15",
			Close:           185.90,
		}},
	})
	if err != nil {
		t.Fatalf("ImportPrices: %v", err)
	}
	if resp.GetJobId() != "job-456" {
		t.Fatalf("expected job_id=job-456, got %s", resp.GetJobId())
	}
	if !enqueued {
		t.Fatal("expected job to be enqueued")
	}
}
