package api

import (
	"context"
	"errors"
	"testing"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func requireGRPCCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := status.Code(err); got != want {
		t.Fatalf("status.Code(err) = %v, want %v", got, want)
	}
}

func authCtx(userID, authSub string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub})
}

func authCtxWithProfile(userID, authSub, name, email string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub, Name: name, Email: email})
}

func TestAPI_Unauthenticated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	srv := NewServer(mock.NewMockDB(ctrl))
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"CreateUser", func() error { _, err := srv.CreateUser(ctx, &apiv1.CreateUserRequest{}); return err }},
		{"ListPortfolios", func() error { _, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{}); return err }},
		{"GetPortfolio", func() error { _, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "any"}); return err }},
		{"CreatePortfolio", func() error { _, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "x"}); return err }},
		{"UpdatePortfolio", func() error { _, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "p", Name: "x"}); return err }},
		{"DeletePortfolio", func() error { _, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "p"}); return err }},
		{"ListTxs", func() error { _, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "p"}); return err }},
		{"GetHoldings", func() error { _, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "p"}); return err }},
		{"GetJob", func() error { _, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"}); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			requireGRPCCode(t, err, codes.Unauthenticated)
		})
	}
}

func TestAPI_InvalidArgument(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	srv := NewServer(mock.NewMockDB(ctrl))
	ctx := authCtx("user-1", "sub|1")
	tests := []struct {
		name string
		call func() error
	}{
		{"GetPortfolio_empty_portfolio_id", func() error { _, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{}); return err }},
		{"CreatePortfolio_empty_name", func() error { _, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{}); return err }},
		{"UpdatePortfolio_empty_portfolio_id", func() error { _, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{Name: "x"}); return err }},
		{"DeletePortfolio_empty_portfolio_id", func() error { _, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{}); return err }},
		{"ListTxs_empty_portfolio_id", func() error { _, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{}); return err }},
		{"GetHoldings_empty_portfolio_id", func() error { _, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{}); return err }},
		{"GetJob_empty_job_id", func() error { _, err := srv.GetJob(ctx, &apiv1.GetJobRequest{}); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			requireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestGetPortfolio_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	requireGRPCCode(t, err, codes.NotFound)
}

func TestCreateUser_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		GetOrCreateUser(gomock.Any(), "sub|1", "Alice", "a@b.com").
		Return("user-123", nil)
	srv := NewServer(db)
	ctx := authCtxWithProfile("", "sub|1", "Alice", "a@b.com")
	resp, err := srv.CreateUser(ctx, &apiv1.CreateUserRequest{AuthSub: "sub|1", Name: "Alice", Email: "a@b.com"})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if resp.GetUserId() != "user-123" {
		t.Fatalf("got user_id %s", resp.GetUserId())
	}
}

func TestCreateUser_DBError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		GetOrCreateUser(gomock.Any(), "sub|1", "A", "a@b.com").
		Return("", errors.New("db error"))
	srv := NewServer(db)
	ctx := authCtx("", "sub|1")
	_, err := srv.CreateUser(ctx, &apiv1.CreateUserRequest{AuthSub: "sub|1", Name: "A", Email: "a@b.com"})
	requireGRPCCode(t, err, codes.Internal)
}

func TestGetPortfolio_Internal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, errors.New("db error"))
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	requireGRPCCode(t, err, codes.Internal)
}

func TestGetPortfolio_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	port := &apiv1.Portfolio{Id: "port-1", Name: "My Portfolio"}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		GetPortfolio(gomock.Any(), "port-1").
		Return(port, "user-1", nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("GetPortfolio: %v", err)
	}
	if resp.GetPortfolio().GetId() != "port-1" || resp.GetPortfolio().GetName() != "My Portfolio" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestCreatePortfolio_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	port := &apiv1.Portfolio{Id: "port-1", Name: "New Portfolio"}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		CreatePortfolio(gomock.Any(), "user-1", "New Portfolio").
		Return(port, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "New Portfolio"})
	if err != nil {
		t.Fatalf("CreatePortfolio: %v", err)
	}
	if resp.GetPortfolio().GetId() != "port-1" || resp.GetPortfolio().GetName() != "New Portfolio" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestCreatePortfolio_DBError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		CreatePortfolio(gomock.Any(), "user-1", "x").
		Return(nil, errors.New("db error"))
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "x"})
	requireGRPCCode(t, err, codes.Internal)
}

func TestListPortfolios_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	portfolios := []*apiv1.Portfolio{{Id: "p1", Name: "P1"}}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		ListPortfolios(gomock.Any(), "user-1", int32(50), "").
		Return(portfolios, "", nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{})
	if err != nil {
		t.Fatalf("ListPortfolios: %v", err)
	}
	if len(resp.GetPortfolios()) != 1 || resp.GetPortfolios()[0].GetId() != "p1" {
		t.Fatalf("got %v", resp.GetPortfolios())
	}
}

func TestListPortfolios_PageSizeClamping(t *testing.T) {
	ctx := authCtx("user-1", "sub|1")
	t.Run("zero_clamps_to_50", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		db := mock.NewMockDB(ctrl)
		db.EXPECT().
			ListPortfolios(gomock.Any(), "user-1", int32(50), "").
			Return(nil, "", nil)
		srv := NewServer(db)
		_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{PageSize: 0})
		if err != nil {
			t.Fatalf("ListPortfolios: %v", err)
		}
	})
	t.Run("over_100_clamps_to_100", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		db := mock.NewMockDB(ctrl)
		db.EXPECT().
			ListPortfolios(gomock.Any(), "user-1", int32(100), "").
			Return(nil, "", nil)
		srv := NewServer(db)
		_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{PageSize: 200})
		if err != nil {
			t.Fatalf("ListPortfolios: %v", err)
		}
	})
}

func TestListPortfolios_DBError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		ListPortfolios(gomock.Any(), "user-1", int32(50), "").
		Return(nil, "", errors.New("db error"))
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{})
	requireGRPCCode(t, err, codes.Internal)
}

func TestUpdatePortfolio_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "port-1", Name: "x"})
	requireGRPCCode(t, err, codes.NotFound)
}

func TestUpdatePortfolio_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	port := &apiv1.Portfolio{Id: "port-1", Name: "Updated"}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		UpdatePortfolio(gomock.Any(), "port-1", "Updated").
		Return(port, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "port-1", Name: "Updated"})
	if err != nil {
		t.Fatalf("UpdatePortfolio: %v", err)
	}
	if resp.GetPortfolio().GetName() != "Updated" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestDeletePortfolio_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "port-1"})
	requireGRPCCode(t, err, codes.NotFound)
}

func TestDeletePortfolio_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		DeletePortfolio(gomock.Any(), "port-1").
		Return(nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("DeletePortfolio: %v", err)
	}
}

func TestListTxs_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "port-1"})
	requireGRPCCode(t, err, codes.NotFound)
}

func TestListTxs_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	txs := []*apiv1.PortfolioTx{{Broker: apiv1.Broker_IBKR, Tx: &apiv1.Tx{InstrumentDescription: "AAPL"}}}
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ListTxs(gomock.Any(), "port-1", (*apiv1.Broker)(nil), (*timestamppb.Timestamp)(nil), (*timestamppb.Timestamp)(nil), int32(50), "").
		Return(txs, "", nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("ListTxs: %v", err)
	}
	if len(resp.GetTxs()) != 1 || resp.GetTxs()[0].GetTx().GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetTxs())
	}
}

func TestGetHoldings_NotFound(t *testing.T) {
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

func TestGetHoldings_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", Quantity: 10}}
	asOf := timestamppb.Now()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ComputeHoldings(gomock.Any(), "port-1", (*timestamppb.Timestamp)(nil)).
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
	if resp.GetAsOf() == nil {
		t.Fatal("asOf should be set")
	}
}

func TestGetJob_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	requireGRPCCode(t, err, codes.NotFound)
}

func TestGetJob_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		GetJob(gomock.Any(), "job-1").
		Return(apiv1.JobStatus_PENDING, nil, "port-1", nil)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	srv := NewServer(db)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if resp.GetStatus() != apiv1.JobStatus_PENDING {
		t.Fatalf("got status %v", resp.GetStatus())
	}
}
