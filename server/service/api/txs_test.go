package api

import (
	"errors"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
)

func TestListTxs_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	txs := []*apiv1.PortfolioTx{{Broker: typev1.Broker_IBKR, Tx: &apiv1.Tx{InstrumentDescription: "AAPL"}}}
	db.EXPECT().
		ListTxs(gomock.Any(), "user-1", (*typev1.Broker)(nil), "", (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), false, int32(50), "").
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
	txs := []*apiv1.PortfolioTx{{Broker: typev1.Broker_IBKR, Tx: &apiv1.Tx{InstrumentDescription: "AAPL"}}}
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ListTxsByPortfolio(gomock.Any(), "port-1", (*typev1.Broker)(nil), (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), false, int32(50), "").
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

// A db failure in the portfolio branch must surface as Internal. The branch
// previously shadowed err, so the failure was reported as an empty OK response.
func TestListTxs_WithPortfolioId_DBError(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ListTxsByPortfolio(gomock.Any(), "port-1", (*typev1.Broker)(nil), (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), false, int32(50), "").
		Return(nil, "", errors.New("boom"))
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

// BROKER_UNSPECIFIED means "all brokers" and must reach the db layer as a nil
// filter, not as a pointer to the zero enum value.
func TestListTxs_BrokerAndDescending(t *testing.T) {
	cases := []struct {
		name       string
		broker     typev1.Broker
		descending bool
		want       *typev1.Broker
	}{
		{"unspecified broker is no filter", typev1.Broker_BROKER_UNSPECIFIED, false, nil},
		{"broker filter with descending", typev1.Broker_FIDELITY, true, typev1.Broker_FIDELITY.Enum()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db := newAPIServerWithMock(t)
			db.EXPECT().
				ListTxs(gomock.Any(), "user-1", tc.want, "", (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), tc.descending, int32(1), "").
				Return(nil, "", nil)
			ctx := authCtx("user-1", "sub|1")
			_, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{
				Broker:     tc.broker,
				Descending: tc.descending,
				PageSize:   1,
			})
			if err != nil {
				t.Fatalf("ListTxs: %v", err)
			}
		})
	}
}
