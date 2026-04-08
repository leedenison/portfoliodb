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
		ListInstrumentsByIDs(gomock.Any(), []string{"broker-only-id"}).
		Return([]*db.InstrumentRow{{ID: "broker-only-id"}}, nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"broker-only-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
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
		ListInstrumentsByIDs(gomock.Any(), []string{"cached-inst-id"}).
		Return([]*db.InstrumentRow{{ID: "cached-inst-id"}}, nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"cached-inst-id", "cached-inst-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
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
		ListInstrumentsByIDs(gomock.Any(), []string{"aapl-id"}).
		Return([]*db.InstrumentRow{{ID: "aapl-id"}}, nil)
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

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// strPtr returns a pointer to s, for use in InstrumentRow.AssetClass.
func strPtr(s string) *string { return &s }

// TestProcessBulk_BuystockIncomeSameDescriptionFails verifies that when a
// BUYSTOCK and an INCOME tx share the same (source, description) and the
// resolved instrument has asset class STOCK, the INCOME row is flagged as a
// contradiction (implied CASH vs resolved STOCK), the whole batch is failed,
// and no transactions are persisted.
func TestProcessBulk_BuystockIncomeSameDescriptionFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "MICROSOFT INC", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: from, InstrumentDescription: "MICROSOFT INC", Type: apiv1.TxType_INCOME, Quantity: 0, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-contradict", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-contradict", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-contradict", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-contradict", int32(2)).
		Return(nil)
	// Both txs share the same (source, description); the description is
	// already linked to a STOCK instrument from a prior upload.
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "MICROSOFT INC").
		Return("msft-stock-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-contradict").
		Return(nil).Times(2)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-contradict", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"msft-stock-id"}).
		Return([]*db.InstrumentRow{{ID: "msft-stock-id", AssetClass: strPtr(db.AssetClassStock)}}, nil)
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-contradict", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 validation error, got %d", len(errs))
				return nil
			}
			if errs[0].RowIndex != 1 {
				t.Errorf("validation error row index = %d, want 1 (INCOME row)", errs[0].RowIndex)
			}
			if errs[0].Field != "type" {
				t.Errorf("validation error field = %q, want %q", errs[0].Field, "type")
			}
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-contradict", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_StockEtfEquivalence verifies that BUYSTOCK resolved to an
// ETF instrument is accepted as compatible (broker-level equivalence).
func TestProcessBulk_StockEtfEquivalence(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "SPY", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-etf", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-etf", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-etf", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-etf", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "SPY").
		Return("spy-etf-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-etf").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-etf", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"spy-etf-id"}).
		Return([]*db.InstrumentRow{{ID: "spy-etf-id", AssetClass: strPtr(db.AssetClassETF)}}, nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"spy-etf-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-etf", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_StockMutualFundNotEquivalent verifies that BUYSTOCK
// resolved to a MUTUAL_FUND instrument is rejected (no transitive
// equivalence through ETF).
func TestProcessBulk_StockMutualFundNotEquivalent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "VFIAX", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-mf", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-mf", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-mf", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-mf", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "VFIAX").
		Return("vfiax-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-mf").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-mf", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"vfiax-id"}).
		Return([]*db.InstrumentRow{{ID: "vfiax-id", AssetClass: strPtr(db.AssetClassMutualFund)}}, nil)
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-mf", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 validation error, got %d", len(errs))
			}
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-mf", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_TransferToCashRejected verifies that an UNKNOWN-implied tx
// (TRANSFER) resolved to a CASH instrument is rejected.
func TestProcessBulk_TransferToCashRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "USD CASH", Type: apiv1.TxType_TRANSFER, Quantity: 10, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-transfer-cash", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-cash", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-transfer-cash", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-transfer-cash", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "USD CASH").
		Return("cash-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-transfer-cash").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-transfer-cash", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"cash-id"}).
		Return([]*db.InstrumentRow{{ID: "cash-id", AssetClass: strPtr(db.AssetClassCash)}}, nil)
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-transfer-cash", gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-cash", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_TransferToStockAllowed verifies that an UNKNOWN-implied tx
// (TRANSFER) resolved to a STOCK instrument is accepted.
func TestProcessBulk_TransferToStockAllowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	to := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: from, InstrumentDescription: "MSFT", Type: apiv1.TxType_TRANSFER, Quantity: 10, Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Broker:     apiv1.Broker_IBKR,
		Source:     "IBKR:test:statement",
		PeriodFrom: from,
		PeriodTo:   to,
		Txs:        txs,
	})
	j := &JobRequest{JobID: "job-transfer-stock", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-stock", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-transfer-stock", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-transfer-stock", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "MSFT").
		Return("msft-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-transfer-stock").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-transfer-stock", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"msft-id"}).
		Return([]*db.InstrumentRow{{ID: "msft-id", AssetClass: strPtr(db.AssetClassStock)}}, nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", gomock.Any(), gomock.Any(), gomock.Any(), []string{"msft-id"}).
		Return(nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-stock", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
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

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}
