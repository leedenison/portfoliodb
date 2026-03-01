package ingestion

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
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

func authCtx(userID string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: "sub|1"})
}

func TestUpsertTxs_Unauthenticated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(mock.NewMockDB(ctrl), queue)
	ctx := context.Background()
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_IBKR,
		Source:      "IBKR:test:statement",
		PeriodFrom:  timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	requireGRPCCode(t, err, codes.Unauthenticated)
}

func TestUpsertTxs_InvalidArgument_portfolioID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(mock.NewMockDB(ctrl), queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: timestamppb.Now(),
		PeriodTo:   timestamppb.Now(),
	})
	requireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_InvalidArgument_broker(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(db, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_BROKER_UNSPECIFIED,
		Source:      "IBKR:test:statement",
		PeriodFrom:  timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	requireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_InvalidArgument_source(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(db, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_IBKR,
		Source:      "", // missing source
		PeriodFrom:  timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	requireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_InvalidArgument_period(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(db, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_IBKR,
		Source:      "IBKR:test:statement",
		PeriodTo:    timestamppb.Now(),
		// PeriodFrom missing
	})
	requireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_PermissionDenied(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(db, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_IBKR,
		Source:      "IBKR:test:statement",
		PeriodFrom:  timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	requireGRPCCode(t, err, codes.PermissionDenied)
}

func TestUpsertTxs_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	periodFrom := timestamppb.Now()
	periodTo := timestamppb.Now()
	db := mock.NewMockDB(ctrl)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		CreateJob(gomock.Any(), "port-1", "IBKR", "IBKR:test:statement", periodFrom, periodTo).
		Return("job-123", nil)
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv := NewServer(db, queue)
	ctx := authCtx("user-1")
	resp, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		PortfolioId: "port-1",
		Broker:      apiv1.Broker_IBKR,
		Source:      "IBKR:test:statement",
		PeriodFrom:  periodFrom,
		PeriodTo:    periodTo,
		Txs:         []*apiv1.Tx{},
	})
	if err != nil {
		t.Fatalf("UpsertTxs: %v", err)
	}
	if resp.GetJobId() != "job-123" {
		t.Fatalf("got job_id %s", resp.GetJobId())
	}
	select {
	case j := <-queue:
		if j.JobID != "job-123" || j.PortfolioID != "port-1" || j.Broker != "IBKR" || j.Source != "IBKR:test:statement" || !j.Bulk {
			t.Fatalf("got JobRequest %+v", j)
		}
	default:
		t.Fatal("expected job on queue")
	}
}
