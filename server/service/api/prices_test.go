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

func TestListPrices_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListPrices(context.Background(), &apiv1.ListPricesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestListPrices_NonAdmin(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestListPrices_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	open := 100.0
	vol := int64(1000)
	rows := []dbpkg.EODPriceRow{
		{
			InstrumentID:          "inst-1",
			InstrumentDisplayName: "AAPL",
			PriceDate:             time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Open:                  &open,
			Close:                 105.0,
			Volume:                &vol,
			DataProvider:          "test",
			FetchedAt:             time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC),
		},
	}
	db.EXPECT().
		ListPrices(gomock.Any(), "", time.Time{}, time.Time{}, "", int32(30), "").
		Return(rows, int32(1), "", nil)
	ctx := adminCtx("user-1", "sub|1")
	resp, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{})
	if err != nil {
		t.Fatalf("ListPrices: %v", err)
	}
	if len(resp.GetPrices()) != 1 {
		t.Fatalf("expected 1 price, got %d", len(resp.GetPrices()))
	}
	p := resp.GetPrices()[0]
	if p.GetInstrumentId() != "inst-1" || p.GetInstrumentDisplayName() != "AAPL" {
		t.Fatalf("got %v", p)
	}
	if p.GetClose() != 105.0 {
		t.Fatalf("expected close=105, got %v", p.GetClose())
	}
	if p.Open == nil || *p.Open != 100.0 {
		t.Fatalf("expected open=100, got %v", p.Open)
	}
	if resp.GetTotalCount() != 1 {
		t.Fatalf("expected total_count=1, got %d", resp.GetTotalCount())
	}
}

func TestListPrices_PageSizeClamping(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPrices(gomock.Any(), "", time.Time{}, time.Time{}, "", int32(100), "").
		Return(nil, int32(0), "", nil)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{PageSize: 200})
	if err != nil {
		t.Fatalf("ListPrices: %v", err)
	}
}

func TestListPrices_DefaultPageSize(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPrices(gomock.Any(), "", time.Time{}, time.Time{}, "", int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{})
	if err != nil {
		t.Fatalf("ListPrices: %v", err)
	}
}

func TestListPrices_DateParsing(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC)
	db.EXPECT().
		ListPrices(gomock.Any(), "", from, to, "", int32(30), "").
		Return(nil, int32(0), "", nil)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{
		DateFrom: "2024-01-01",
		DateTo:   "2024-01-31",
	})
	if err != nil {
		t.Fatalf("ListPrices: %v", err)
	}
}

func TestListPrices_InvalidDate(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{DateFrom: "not-a-date"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestListPrices_DBError(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPrices(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, int32(0), "", context.DeadlineExceeded)
	ctx := adminCtx("user-1", "sub|1")
	_, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}
