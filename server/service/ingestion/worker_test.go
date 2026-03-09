package ingestion

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
	j := &JobRequest{
		JobID:      "job-1",
		UserID:     "user-1",
		Broker:     "IBKR",
		Source:     "IBKR:test:statement",
		Bulk:       true,
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	}

	// processBulk: SetJobStatus RUNNING already done in processJob
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).
		Return(nil)
	// Resolve for "UNKNOWN": DB miss, nil descRegistry → extraction failed, EnsureInstrument broker-only
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "UNKNOWN", Canonical: false}}, "", nil, nil).
		Return("broker-only-id", nil)
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
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", from, to, txs, []string{"broker-only-id"}).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j)
}

func TestProcessBulk_BatchCache_ResolvesSameDescriptionOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	// Two txs with same instrument description - should resolve once and use cache for second
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.New(from.AsTime().Add(-1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: ""},
		{Timestamp: timestamppb.New(from.AsTime().Add(1)), InstrumentDescription: "CACHED", Type: apiv1.TxType_BUYSTOCK, Quantity: 2, Account: ""},
	}
	j := &JobRequest{
		JobID:      "job-2",
		UserID:     "user-1",
		Broker:     "IBKR",
		Source:     "IBKR:test:statement",
		Bulk:       true,
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_RUNNING).
		Return(nil)
	// First resolve: DB miss, nil descRegistry → extraction failed, EnsureInstrument
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "CACHED").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "CACHED", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "CACHED", Canonical: false}}, "", nil, nil).
		Return("cached-inst-id", nil)
	// Second tx hits cache - no additional DB calls
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-2", gomock.Any()).
		Return(nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", from, to, txs, []string{"cached-inst-id", "cached-inst-id"}).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j)
}

func TestProcessBulk_DropsSecurityTypeNoneTransactions(t *testing.T) {
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
	j := &JobRequest{
		JobID:      "job-split",
		UserID:     "user-1",
		Broker:     "IBKR",
		Source:     "IBKR:test:statement",
		Bulk:       true,
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-split", apiv1.JobStatus_RUNNING).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "AAPL").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "AAPL", gomock.Any(), "", nil, nil).
		Return("aapl-id", nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-split", gomock.Any()).
		Return(nil)
	// ReplaceTxsInPeriod must be called with only the non-None tx (AAPL), not the SPLIT
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", from, to, gomock.Len(1), []string{"aapl-id"}).
		DoAndReturn(func(_ context.Context, _, _ string, _, _ *timestamppb.Timestamp, storedTxs []*apiv1.Tx, ids []string) error {
			if len(storedTxs) != 1 || storedTxs[0].InstrumentDescription != "AAPL" || storedTxs[0].Type != apiv1.TxType_BUYSTOCK {
				t.Errorf("ReplaceTxsInPeriod called with %d txs, expected 1 (AAPL BUYSTOCK)", len(storedTxs))
			}
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-split", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, database, registry, nil, nil, j)
}

func TestProcessSingle_DropsSecurityTypeNoneTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	j := &JobRequest{
		JobID:  "job-single-split",
		UserID: "user-1",
		Broker: "IBKR",
		Source: "IBKR:test:statement",
		Bulk:   false,
		Tx:     &apiv1.Tx{Timestamp: timestamppb.Now(), InstrumentDescription: "SPLIT", Type: apiv1.TxType_SPLIT, Quantity: 1, Account: ""},
	}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-split", apiv1.JobStatus_RUNNING).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-split", apiv1.JobStatus_SUCCESS).
		Return(nil)
	// No Resolve, no CreateTx - SPLIT is dropped

	processJob(ctx, database, registry, nil, nil, j)
}
