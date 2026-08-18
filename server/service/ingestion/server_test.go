package ingestion

import (
	"context"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
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

func TestUpsertTxs(t *testing.T) {
	now := timestamppb.Now()
	tests := []struct {
		name     string
		ctx      context.Context
		req      *ingestionv1.UpsertTxsRequest
		wantCode codes.Code
	}{
		{"Unauthenticated", context.Background(), &ingestionv1.UpsertTxsRequest{
			Window: &archivev1.TxWindow{
				Broker:       typev1.Broker_IBKR,
				Source:       "IBKR:test:statement",
				PeriodFrom:   now,
				PeriodBefore: now,
			},
		}, codes.Unauthenticated},
		{"InvalidArgument_broker", authCtx("user-1"), &ingestionv1.UpsertTxsRequest{
			Window: &archivev1.TxWindow{
				Broker:       typev1.Broker_BROKER_UNSPECIFIED,
				Source:       "IBKR:test:statement",
				PeriodFrom:   now,
				PeriodBefore: now,
			},
		}, codes.InvalidArgument},
		{"InvalidArgument_source", authCtx("user-1"), &ingestionv1.UpsertTxsRequest{
			Window: &archivev1.TxWindow{
				Broker:       typev1.Broker_IBKR,
				Source:       "",
				PeriodFrom:   now,
				PeriodBefore: now,
			},
		}, codes.InvalidArgument},
		{"InvalidArgument_period", authCtx("user-1"), &ingestionv1.UpsertTxsRequest{
			Window: &archivev1.TxWindow{
				Broker:       typev1.Broker_IBKR,
				Source:       "IBKR:test:statement",
				PeriodBefore: now,
			},
		}, codes.InvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			queue := make(chan *JobRequest, 1)
			defer close(queue)
			srv, _ := newIngestionServerWithMock(t, queue)
			_, err := srv.UpsertTxs(tc.ctx, tc.req)
			testutil.RequireGRPCCode(t, err, tc.wantCode)
		})
	}
}

func TestUpsertTxs_Success(t *testing.T) {
	periodFrom := timestamppb.Now()
	periodBefore := timestamppb.Now()
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, mockDB := newIngestionServerWithMock(t, queue)
	mockDB.EXPECT().
		CreateJob(gomock.Any(), gomock.AssignableToTypeOf(db.CreateJobParams{})).
		DoAndReturn(func(_ context.Context, p db.CreateJobParams) (string, error) {
			if p.UserID != "user-1" || p.Broker != "IBKR" || p.Source != "IBKR:test:statement" || p.JobType != "tx" {
				t.Errorf("CreateJob params: %+v", p)
			}
			if len(p.Payload) == 0 {
				t.Error("expected non-empty payload")
			}
			return "job-123", nil
		})
	ctx := authCtx("user-1")
	resp, err := srv.UpsertTxs(ctx, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   periodFrom,
			PeriodBefore: periodBefore,
		},
	})
	if err != nil {
		t.Fatalf("UpsertTxs: %v", err)
	}
	if resp.GetJobId() != "job-123" {
		t.Fatalf("got job_id %s", resp.GetJobId())
	}
	select {
	case j := <-queue:
		if j.JobID != "job-123" || j.JobType != "tx" {
			t.Fatalf("got JobRequest %+v", j)
		}
	default:
		t.Fatal("expected job on queue")
	}
}

// An upload that states no vintage is its own export, so the payload leaves the
// handler carrying this server's clock. It is stamped here rather than read when
// the job runs because a job re-enqueued by the restart recovery path would
// otherwise take the vintage of the retry.
func TestUpsertTxs_StampsTheReceiptTimeWhenTheUploadStatesNoVintage(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, mockDB := newIngestionServerWithMock(t, queue)

	var payload []byte
	mockDB.EXPECT().
		CreateJob(gomock.Any(), gomock.AssignableToTypeOf(db.CreateJobParams{})).
		DoAndReturn(func(_ context.Context, p db.CreateJobParams) (string, error) {
			payload = p.Payload
			return "job-123", nil
		}).Times(1)

	req := &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   timestamppb.Now(),
			PeriodBefore: timestamppb.Now(),
		},
	}
	before := time.Now()
	if _, err := srv.UpsertTxs(authCtx("user-1"), req); err != nil {
		t.Fatalf("UpsertTxs: %v", err)
	}
	if req.GetExportedAt() != nil {
		t.Error("the caller's request was mutated; it belongs to the gRPC call")
	}

	var stored ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	got := stored.GetExportedAt()
	if got == nil {
		t.Fatal("payload states no exported_at, so the vintage would have to be guessed")
	}
	if got.AsTime().Before(before) || got.AsTime().After(time.Now()) {
		t.Errorf("exported_at = %v, want the receipt time", got.AsTime())
	}
}

// An upload that does state its vintage keeps it: the file knows when it was
// written and this server does not.
func TestUpsertTxs_KeepsAStatedVintage(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, mockDB := newIngestionServerWithMock(t, queue)

	var payload []byte
	mockDB.EXPECT().
		CreateJob(gomock.Any(), gomock.AssignableToTypeOf(db.CreateJobParams{})).
		DoAndReturn(func(_ context.Context, p db.CreateJobParams) (string, error) {
			payload = p.Payload
			return "job-123", nil
		}).Times(1)

	stated := timestamppb.New(time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC))
	if _, err := srv.UpsertTxs(authCtx("user-1"), &ingestionv1.UpsertTxsRequest{
		ExportedAt: stated,
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   timestamppb.Now(),
			PeriodBefore: timestamppb.Now(),
		},
	}); err != nil {
		t.Fatalf("UpsertTxs: %v", err)
	}

	var stored ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !stored.GetExportedAt().AsTime().Equal(stated.AsTime()) {
		t.Errorf("exported_at = %v, want the stated %v", stored.GetExportedAt().AsTime(), stated.AsTime())
	}
}

// Manual entry names an instrument under the name it wears now, which is what
// the append's own receipt time says.
func TestCreateTx_StampsTheReceiptTime(t *testing.T) {
	queue := make(chan *JobRequest, 1)
	defer close(queue)
	srv, mockDB := newIngestionServerWithMock(t, queue)

	var payload []byte
	mockDB.EXPECT().
		CreateJob(gomock.Any(), gomock.AssignableToTypeOf(db.CreateJobParams{})).
		DoAndReturn(func(_ context.Context, p db.CreateJobParams) (string, error) {
			payload = p.Payload
			return "job-124", nil
		}).Times(1)

	if _, err := srv.CreateTx(authCtx("user-1"), &ingestionv1.CreateTxRequest{
		Broker: typev1.Broker_IBKR,
		Source: "IBKR:test:manual",
		Posting: &archivev1.Posting{
			TradeDate:             timestamppb.Now(),
			Account:               "acct",
			InstrumentDescription: "AAPL",
			BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
			Quantity:              "1",
		},
	}); err != nil {
		t.Fatalf("CreateTx: %v", err)
	}

	var stored ingestionv1.UpsertTxsRequest
	if err := proto.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if stored.GetExportedAt() == nil {
		t.Error("payload states no exported_at, so the vintage would have to be guessed")
	}
}
