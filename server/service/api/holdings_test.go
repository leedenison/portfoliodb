package api

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/db/mock"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetHoldings_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", Quantity: 10}}
	asOf := timestamppb.Now()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		ComputeHoldings(gomock.Any(), "user-1", (*apiv1.Broker)(nil), "", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	srv := NewServer(db)
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", Quantity: 10}}
	asOf := timestamppb.Now()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ComputeHoldingsForPortfolio(gomock.Any(), "port-1", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	srv := NewServer(db)
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
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "port-1"})
	requireGRPCCode(t, err, codes.NotFound)
}
