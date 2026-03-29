package ingestion

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// marshalPayload serializes an UpsertTxsRequest for test fixtures.
func marshalPayload(t *testing.T, req *ingestionv1.UpsertTxsRequest) []byte {
	t.Helper()
	b, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// expectLoadPayload sets up LoadJobPayload + ClearJobPayload + GetJob(userID) + ListIgnoredAssetClasses mocks.
func expectLoadPayload(database *mock.MockDB, jobID, userID string, payload []byte) {
	database.EXPECT().LoadJobPayload(gomock.Any(), jobID).Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), jobID).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), jobID).Return(
		apiv1.JobStatus_RUNNING, nil, nil, userID, int32(0), int32(0), nil,
	)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), userID).Return(nil, nil)
}

func TestProcessBulk_AppendsIdentificationErrorsWhenBrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry() // no plugins

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "UNKNOWN", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-1", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-1", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-1", int32(1)).
		Return(nil)
	// Resolve for "UNKNOWN": DB miss, nil descRegistry -> extraction failed, EnsureInstrument broker-only
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "UNKNOWN", Canonical: false}}, "", nil, nil).
		Return("broker-only-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-1").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []db.IdentificationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 identification error, got %d", len(errs))
				return nil
			}
			if errs[0].Message != MsgExtractionFailed {
				t.Errorf("identification error message = %q, want %q", errs[0].Message, MsgExtractionFailed)
			}
			if errs[0].InstrumentDescription != "UNKNOWN" {
				t.Errorf("instrument description = %q, want UNKNOWN", errs[0].InstrumentDescription)
			}
			return nil
		})
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"broker-only-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j, nil)
}

func TestProcessBulk_BatchCache_ResolvesSameDescriptionOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(from.AsTime().Add(-1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: ""},
		{Timestamp: timestamppb.New(from.AsTime().Add(1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 2, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-2", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-2", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-2", int32(2)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "CACHED").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "CACHED", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "CACHED", Canonical: false}}, "", nil, nil).
		Return("cached-inst-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-2").
		Return(nil).Times(2)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-2", gomock.Any()).
		Return(nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"cached-inst-id", "cached-inst-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j, nil)
}

func TestProcessBulk_DropsTxTypeSplitTransactions(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: from, InstrumentDescription: "SPLIT", Type: apiv1.TxType_SPLIT, Quantity: 1, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-split", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-split", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-split", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-split", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "AAPL").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "AAPL", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		Return("aapl-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-split").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-split", gomock.Any()).
		Return(nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Len(1), []string{"aapl-id"}).
		DoAndReturn(func(_ context.Context, _, _ string, _, _ *timestamppb.Timestamp, storedTxs []*apiv1.Tx, ids []string) error {
			if len(storedTxs) != 1 || storedTxs[0].InstrumentDescription != "AAPL" || storedTxs[0].Type != apiv1.TxType_BUYSTOCK {
				t.Errorf("ReplaceTxsInPeriod called with %d txs, expected 1 (AAPL BUYSTOCK)", len(storedTxs))
			}
			return nil
		})
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-split", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j, nil)
}

func TestProcessSingle_DropsTxTypeSplitTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker: apiv1.Broker_IBKR,
		Source: "IBKR:test:statement",
		Txs:    []*apiv1.Tx{{Timestamp: timestamppb.Now(), InstrumentDescription: "SPLIT", Type: apiv1.TxType_SPLIT, Quantity: 1, Account: ""}},
	})
	j := &JobRequest{JobID: "job-single-split", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-split", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-single-split", "user-1", payload)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-split", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j, nil)
}
