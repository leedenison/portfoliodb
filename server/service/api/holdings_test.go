package api

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetHoldings_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", Quantity: 10}}
	asOf := timestamppb.Now()
	db.EXPECT().
		ComputeHoldings(gomock.Any(), "user-1", (*apiv1.Broker)(nil), "", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{})
	if err != nil {
		t.Fatalf("GetHoldings: %v", err)
	}
	if len(resp.GetHoldings()) != 1 || resp.GetHoldings()[0].GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetHoldings())
	}
	if resp.GetAsOf() == nil {
		t.Fatal("asOf should be set")
	}
}

func TestGetHoldings_WithPortfolioId_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", Quantity: 10}}
	asOf := timestamppb.Now()
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ComputeHoldingsForPortfolio(gomock.Any(), "port-1", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("GetHoldings: %v", err)
	}
	if len(resp.GetHoldings()) != 1 || resp.GetHoldings()[0].GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetHoldings())
	}
}

func TestGetHoldings_WithPortfolioId_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}
