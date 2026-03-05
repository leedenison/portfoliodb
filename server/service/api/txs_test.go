package api

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestListTxs_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	txs := []*apiv1.PortfolioTx{{Broker: apiv1.Broker_IBKR, Tx: &apiv1.Tx{InstrumentDescription: "AAPL"}}}
	db.EXPECT().
		ListTxs(gomock.Any(), "user-1", (*apiv1.Broker)(nil), "", (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), int32(50), "").
		Return(txs, "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{})
	if err != nil {
		t.Fatalf("ListTxs: %v", err)
	}
	if len(resp.GetTxs()) != 1 || resp.GetTxs()[0].GetTx().GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetTxs())
	}
}

func TestListTxs_WithPortfolioId_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	txs := []*apiv1.PortfolioTx{{Broker: apiv1.Broker_IBKR, Tx: &apiv1.Tx{InstrumentDescription: "AAPL"}}}
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ListTxsByPortfolio(gomock.Any(), "port-1", (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), int32(50), "").
		Return(txs, "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("ListTxs: %v", err)
	}
	if len(resp.GetTxs()) != 1 || resp.GetTxs()[0].GetTx().GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetTxs())
	}
}

func TestListTxs_WithPortfolioId_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}
