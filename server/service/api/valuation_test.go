package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetPortfolioValuation_Success(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("USD", nil)
	mdb.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)

	dateFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	mdb.EXPECT().
		GetPortfolioValuation(gomock.Any(), "port-1", dateFrom, dateTo, "USD").
		Return([]db.ValuationPoint{
			{Date: dateFrom, TotalValue: 1000.0, UnpricedInstruments: nil},
			{Date: dateFrom.AddDate(0, 0, 1), TotalValue: 1050.0, UnpricedInstruments: []string{"UNKNOWN CORP"}},
			{Date: dateTo, TotalValue: 1100.0, UnpricedInstruments: nil},
		}, nil)

	resp, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{
		PortfolioId: "port-1",
		DateFrom:    "2025-01-01",
		DateTo:      "2025-01-03",
	})
	if err != nil {
		t.Fatalf("GetPortfolioValuation: %v", err)
	}
	if len(resp.GetPoints()) != 3 {
		t.Fatalf("expected 3 points, got %d", len(resp.GetPoints()))
	}
	if resp.Points[0].Date != "2025-01-01" || resp.Points[0].TotalValue != 1000.0 {
		t.Fatalf("unexpected first point: %+v", resp.Points[0])
	}
	if len(resp.Points[1].UnpricedInstruments) != 1 || resp.Points[1].UnpricedInstruments[0] != "UNKNOWN CORP" {
		t.Fatalf("unexpected unpriced instruments: %v", resp.Points[1].UnpricedInstruments)
	}
}

func TestGetPortfolioValuation_PortfolioNotFound(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("USD", nil)
	mdb.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)

	_, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{
		PortfolioId: "port-1",
		DateFrom:    "2025-01-01",
		DateTo:      "2025-01-03",
	})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestGetPortfolioValuation_InvalidArgument(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	tests := []struct {
		name string
		req  *apiv1.GetPortfolioValuationRequest
	}{
		{"bad_date_from", &apiv1.GetPortfolioValuationRequest{
			PortfolioId: "port-1", DateFrom: "bad", DateTo: "2025-01-03",
		}},
		{"bad_date_to", &apiv1.GetPortfolioValuationRequest{
			PortfolioId: "port-1", DateFrom: "2025-01-01", DateTo: "bad",
		}},
		{"date_to_before_date_from", &apiv1.GetPortfolioValuationRequest{
			PortfolioId: "port-1", DateFrom: "2025-01-03", DateTo: "2025-01-01",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.GetPortfolioValuation(ctx, tc.req)
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGetPortfolioValuation_DBError(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("USD", nil)
	mdb.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	mdb.EXPECT().
		GetPortfolioValuation(gomock.Any(), "port-1", gomock.Any(), gomock.Any(), "USD").
		Return(nil, fmt.Errorf("db boom"))

	_, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{
		PortfolioId: "port-1",
		DateFrom:    "2025-01-01",
		DateTo:      "2025-01-03",
	})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestGetUserValuation_Success(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("USD", nil)

	dateFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)
	mdb.EXPECT().
		GetUserValuation(gomock.Any(), "user-1", dateFrom, dateTo, "USD").
		Return([]db.ValuationPoint{
			{Date: dateFrom, TotalValue: 2000.0, UnpricedInstruments: nil},
			{Date: dateTo, TotalValue: 2100.0, UnpricedInstruments: nil},
		}, nil)

	resp, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{
		DateFrom: "2025-01-01",
		DateTo:   "2025-01-03",
	})
	if err != nil {
		t.Fatalf("GetPortfolioValuation (user): %v", err)
	}
	if len(resp.GetPoints()) != 2 {
		t.Fatalf("expected 2 points, got %d", len(resp.GetPoints()))
	}
	if resp.Points[0].TotalValue != 2000.0 {
		t.Fatalf("unexpected first point value: %v", resp.Points[0].TotalValue)
	}
}

func TestGetUserValuation_DBError(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("USD", nil)
	mdb.EXPECT().
		GetUserValuation(gomock.Any(), "user-1", gomock.Any(), gomock.Any(), "USD").
		Return(nil, fmt.Errorf("db boom"))

	_, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{
		DateFrom: "2025-01-01",
		DateTo:   "2025-01-03",
	})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}
