package ingestion

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func authCtx(userID string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: "sub|1"})
}

// newIngestionServerWithMock creates a gomock controller, mock DB, and ingestion server. The controller is finished when the test ends.
func newIngestionServerWithMock(t *testing.T, queue chan<- *JobRequest) (*Server, *mock.MockDB) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	db := mock.NewMockDB(ctrl)
	return NewServer(db, queue), db
}

func TestUpsertTxs_Unauthenticated(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, _ := newIngestionServerWithMock(t, queue)
	ctx := context.Background()
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: timestamppb.Now(),
		PeriodTo:   timestamppb.Now(),
	})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestUpsertTxs_InvalidArgument_broker(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, _ := newIngestionServerWithMock(t, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:      apiv1.Broker_BROKER_UNSPECIFIED,
		Source:      "IBKR:test:statement",
		PeriodFrom:  timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_InvalidArgument_source(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, _ := newIngestionServerWithMock(t, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "", // missing source
		PeriodFrom: timestamppb.Now(),
		PeriodTo:    timestamppb.Now(),
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_InvalidArgument_period(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, _ := newIngestionServerWithMock(t, queue)
	ctx := authCtx("user-1")
	_, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:   apiv1.Broker_IBKR,
		Source:   "IBKR:test:statement",
		PeriodTo: timestamppb.Now(),
		// PeriodFrom missing
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpsertTxs_Success(t *testing.T) {
	periodFrom := timestamppb.Now()
	periodTo := timestamppb.Now()
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, db := newIngestionServerWithMock(t, queue)
	db.EXPECT().
		CreateJob(gomock.Any(), "user-1", "IBKR", "IBKR:test:statement", periodFrom, periodTo).
		Return("job-123", nil)
	ctx := authCtx("user-1")
	resp, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: periodFrom,
		PeriodTo:   periodTo,
		Txs:        []*apiv1.Tx{},
	})
	if err != nil {
		t.Fatalf("UpsertTxs: %v", err)
	}
	if resp.GetJobId() != "job-123" {
		t.Fatalf("got job_id %s", resp.GetJobId())
	}
	select {
	case j := <-queue:
		if j.JobID != "job-123" || j.UserID != "user-1" || j.Broker != "IBKR" || j.Source != "IBKR:test:statement" || !j.Bulk {
			t.Fatalf("got JobRequest %+v", j)
		}
	default:
		t.Fatal("expected job on queue")
	}
}
