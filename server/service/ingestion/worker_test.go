package ingestion

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
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

// expectLoadPayload sets up LoadJobPayload + ClearJobPayload + GetJob(userID) mocks.
func expectLoadPayload(database *mock.MockDB, jobID, userID string, payload []byte) {
	database.EXPECT().LoadJobPayload(gomock.Any(), jobID).Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), jobID).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), jobID).Return(
		&db.JobDetail{Status: apiv1.JobStatus_RUNNING, UserID: userID}, nil,
	)
}

func TestProcessBulk_AppendsIdentificationErrorsWhenBrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry() // no plugins

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "UNKNOWN", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
	})
	j := &JobRequest{JobID: "job-1", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-1", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-1", int32(1)).
		Return(nil)
	// Resolve for "UNKNOWN": DB miss, nil candRegistry -> extraction failed, EnsureInstrument broker-only
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "UNKNOWN", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "UNKNOWN", Domain: "IBKR:test:statement"},
			Canonical: false,
		}}, gomock.Any(), "", nil).
		Return("broker-only-id", "listing-id", nil)
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
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		InstrumentsWithSplits(gomock.Any(), gomock.Any()).
		Return(nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: timestamppb.New(from.AsTime().Add(-1)),
			TradeDate: timestamppb.New(from.AsTime().Add(-1)), InstrumentDescription: "CACHED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1", Account: ""},
		{OrderDate: timestamppb.New(from.AsTime().Add(1)),
			TradeDate: timestamppb.New(from.AsTime().Add(1)), InstrumentDescription: "CACHED", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "2", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		EnsureInstrument(gomock.Any(), "", "", "CACHED", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "CACHED", Domain: "IBKR:test:statement"},
			Canonical: false,
		}}, gomock.Any(), "", nil).
		Return("cached-inst-id", "listing-id", nil)
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
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-2", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		InstrumentsWithSplits(gomock.Any(), gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// strPtr returns a pointer to s, for use in InstrumentRow.AssetClass.
func strPtr(s string) *string { return &s }

// TestProcessBulk_StatedCashOnStockInstrumentFails verifies that when a stock
// buy and an income tx share the same (source, description) and the resolved
// instrument has asset class STOCK, the income row is flagged as a
// contradiction (stated CASH vs resolved STOCK), the whole batch is failed,
// and no transactions are persisted.
func TestProcessBulk_StatedCashOnStockInstrumentFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "MICROSOFT INC", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, AssetClassHint: typev1.AssetClass_STOCK, Quantity: "10", Account: ""},
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "MICROSOFT INC", BrokerTxType: []typev1.TxType{typev1.TxType_INCOME}, AssetClassHint: typev1.AssetClass_CASH, Quantity: "0", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		AppendValidationErrors(gomock.Any(), "job-contradict", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ archivev1.ArchivePart, errs []*apiv1.ValidationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 validation error, got %d", len(errs))
				return nil
			}
			if errs[0].RowIndex != 1 {
				t.Errorf("validation error row index = %d, want 1 (income row)", errs[0].RowIndex)
			}
			if errs[0].Field != "asset_class_hint" {
				t.Errorf("validation error field = %q, want %q", errs[0].Field, "asset_class_hint")
			}
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-contradict", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_SpecificHintMeansWhatItSays is the other side of the coarse
// value existing: a source that said STOCK claimed a share and not a fund, so a
// resolution to an ETF contradicts it. Before EQUITY was a value it could
// state, this had to be tolerated and the tolerance was invisible here.
func TestProcessBulk_SpecificHintMeansWhatItSays(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "SPY", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, AssetClassHint: typev1.AssetClass_STOCK, Quantity: "10", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
	})
	j := &JobRequest{JobID: "job-stock-etf", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-stock-etf", apiv1.JobStatus_RUNNING).
		Return(nil)
	expectLoadPayload(database, "job-stock-etf", "user-1", payload)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-stock-etf", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "SPY").
		Return("spy-etf-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-stock-etf").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-stock-etf", gomock.Any()).
		Times(0)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"spy-etf-id"}).
		Return([]*db.InstrumentRow{{ID: "spy-etf-id", AssetClass: strPtr(db.AssetClassETF)}}, nil)
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-stock-etf", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ archivev1.ArchivePart, errs []*apiv1.ValidationError) error {
			if len(errs) != 1 {
				t.Errorf("expected 1 validation error, got %d", len(errs))
				return nil
			}
			if errs[0].Field != "asset_class_hint" {
				t.Errorf("validation error field = %q, want %q", errs[0].Field, "asset_class_hint")
			}
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-stock-etf", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_CoarseHintAdmitsItsLeaf verifies that a source that could not
// tell a share from a fund, and said so, is not contradicted by a resolution to
// either. The row states EQUITY and the description is already linked to an ETF.
func TestProcessBulk_CoarseHintAdmitsItsLeaf(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "SPY", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, AssetClassHint: typev1.AssetClass_EQUITY, Quantity: "10", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-etf", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		InstrumentsWithSplits(gomock.Any(), gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-etf", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_StockMutualFundNotEquivalent verifies that a stated STOCK
// resolved to a MUTUAL_FUND instrument is rejected (no transitive
// equivalence through ETF).
func TestProcessBulk_StockMutualFundNotEquivalent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "VFIAX", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, AssetClassHint: typev1.AssetClass_STOCK, Quantity: "10", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		AppendValidationErrors(gomock.Any(), "job-mf", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ archivev1.ArchivePart, errs []*apiv1.ValidationError) error {
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

// TestProcessBulk_TransferToCashRejected verifies that a transfer stating
// SECURITY -- a security of unstated class -- resolved to a CASH instrument is
// rejected. Money and securities are disjoint branches, so this falls out of
// the vocabulary rather than needing a rule about UNKNOWN of its own.
func TestProcessBulk_TransferToCashRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "USD CASH", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, AssetClassHint: typev1.AssetClass_SECURITY, Quantity: "10", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		AppendValidationErrors(gomock.Any(), "job-transfer-cash", gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-cash", apiv1.JobStatus_FAILED).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// TestProcessBulk_TransferToStockAllowed verifies that a transfer stating
// UNKNOWN resolved to a STOCK instrument is accepted.
func TestProcessBulk_TransferToStockAllowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "MSFT", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, AssetClassHint: typev1.AssetClass_UNKNOWN, Quantity: "10", Account: ""},
	}
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_IBKR,
			Source:       "IBKR:test:statement",
			PeriodFrom:   from,
			PeriodBefore: before,
			Postings:     postings,
		},
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
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-transfer-stock", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		InstrumentsWithSplits(gomock.Any(), gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-transfer-stock", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// A file names an option under the symbol current when the file was written, so
// the vintage the resolved name is dated from is the upload's own exported_at
// and not each posting's trade date. The observable is the valid_from written
// with the name: dating it from the trade date would claim the name was correct
// before the file that stated it existed, and would hand the retroactive
// option-split pass a symbol to restate that has already been restated.
func TestProcessTx_DatesTheNameFromTheUploadVintageNotTheTradeDate(t *testing.T) {
	const occ = "AAPL250117C00760000"
	tradeDate := timestamppb.New(time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))

	for _, exportedAt := range []time.Time{
		// Either side of the 2024-08-01 ex_date this file's contract saw, which
		// is the ordinary shape of a broker file covering a split. Neither is the
		// trade date, and that is the whole assertion.
		time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
	} {
		t.Run(exportedAt.Format("2006-01-02"), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			database := mock.NewMockDB(ctrl)
			database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
			database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			registry := identifier.NewRegistry()
			registry.Register("local", &fakePlugin{
				inst: &identifier.Instrument{
					AssetClass: "OPTION",
					Listing:    identifier.Listing{Currency: "USD"},
					// A contract is written on a line of its underlying, so a
					// stored option needs one; without it the resolution degrades
					// to a broker-description-only instrument (adr/0074).
					UnderlyingIdentifiers: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
				},
				ids: []identifier.Identifier{{Type: "OCC", Value: occ}},
			})
			// The underlying short-circuits out of the instrument table, and the
			// contract's USD strike names the line of it the option delivers.
			database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
				Return("underlying-id", "STOCK", []string{"USD"}, nil).AnyTimes()
			database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("underlying-id", nil).AnyTimes()
			database.EXPECT().EnsureListing(gomock.Any(), "underlying-id", "USD").Return("underlying-line-id", nil).AnyTimes()

			payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
				ExportedAt: timestamppb.New(exportedAt),
				Window: &archivev1.TxWindow{
					Broker:       typev1.Broker_IBKR,
					Source:       "IBKR:test:statement",
					PeriodFrom:   tradeDate,
					PeriodBefore: timestamppb.New(time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)),
					Postings: []*archivev1.Posting{{
						OrderDate:             tradeDate,
						TradeDate:             tradeDate,
						InstrumentDescription: "AAPL 250117C00760000",
						BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
						Quantity:              "1",
						IdentifierHints: []*archivev1.InstrumentRef{
							{Type: typev1.IdentifierType_OCC, Value: occ},
						},
					}},
				},
			})

			database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).Return(nil)
			expectLoadPayload(database, "job-1", "user-1", payload)
			database.EXPECT().SetJobTotalCount(gomock.Any(), "job-1", int32(1)).Return(nil)
			// The posting states an OCC, so the pre-pass asks the identifier
			// question first, and then asks the description whether it already
			// names an instrument with no identity -- the one a hinted upload
			// used to fork away from. Here it does not.
			//
			// The hint reaches every lookup as the file spelled it: nothing
			// rewrites an OCC on its way to a provider or to the instrument table.
			database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "OCC", "", occ).Return("", nil)
			database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), "IBKR:test:statement", "AAPL 250117C00760000").Return("", nil)
			database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", occ).Return("", "", nil, nil)
			database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "OCC", occ).Return("", nil).AnyTimes()
			database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
				Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10}}, nil)

			// The assertion.
			var validFrom []*time.Time
			database.EXPECT().EnsureInstrument(gomock.Any(), "OPTION", "USD", "", "", "", gomock.Any(), gomock.Any(), "underlying-line-id", gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _ *db.OptionFields) (string, string, error) {
					for _, idn := range idns {
						validFrom = append(validFrom, idn.ValidFrom)
					}
					return "option-id", "listing-id", nil
				})
			database.EXPECT().AppendIdentificationErrors(gomock.Any(), "job-1", gomock.Any()).Return(nil).AnyTimes()
			database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-1").Return(nil)
			database.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{"option-id"}).
				Return([]*db.InstrumentRow{{ID: "option-id"}}, nil)
			database.EXPECT().ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil)
			database.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
			database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).Return(nil)

			processJob(context.Background(), WorkerOptions{DB: database, IdentifierRegistry: registry}, &JobRequest{JobID: "job-1", JobType: "tx"})

			if len(validFrom) == 0 {
				t.Fatal("no identifier was written")
			}
			for _, got := range validFrom {
				if got == nil || !got.Equal(exportedAt) {
					t.Errorf("valid_from = %v, want the upload vintage %v", got, exportedAt)
				}
			}
		})
	}
}

// --- proposeCandidates ---

// hintedTx is a posting that states an identifier.
func hintedTx(desc string, hints ...*apiv1.InstrumentIdentifier) *apiv1.Tx {
	return &apiv1.Tx{
		InstrumentDescription: desc,
		AssetClassHint:        typev1.AssetClass_STOCK,
		IdentifierHints:       hints,
	}
}

// A posting that states an identifier is looked up by it first, and reaches the
// description only if that misses. Here it does not miss, so the description is
// never asked: an instrument the identifiers already name has an identity, and
// the description question is only ever about one that has none.
func TestProposeCandidates_HintedKeyIsLookedUpByItsIdentifiers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	// No FindInstrumentBySourceDescription expectation: calling it fails the test.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("aapl-id", nil)

	txs := []*apiv1.Tx{hintedTx("APPLE INC", &apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0378331005"})}
	txHints := [][]identifier.Identifier{{{Type: "ISIN", Value: "US0378331005"}}}

	pre, err := proposeCandidates(context.Background(), ingestDeps{DB: database}, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", txHints[0])
	if pre.resolved[key].InstrumentID != "aapl-id" {
		t.Errorf("pre.resolved[key].InstrumentID = %q, want aapl-id", pre.resolved[key].InstrumentID)
	}
	// Which lookup answered has to travel with the answer: a description and a
	// set of identifiers are different questions and must not be recorded alike.
	if pre.resolved[key].DBHitOutcome != db.TelemetryResolutionDBIdentifierHints {
		t.Errorf("DBHitOutcome = %q, want %q", pre.resolved[key].DBHitOutcome, db.TelemetryResolutionDBIdentifierHints)
	}
	if pre.outcome[key] != db.TelemetryCandidateNotAttemptedDBHit {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateNotAttemptedDBHit)
	}
	if len(pre.conflicts) != 0 {
		t.Errorf("conflicts = %v, want none", pre.conflicts)
	}
}

// A broker-description-only instrument is findable by nothing but its
// description, so the identifier lookup passes straight over the one already
// holding this key's transactions. Issue 0135: without the second lookup the
// upload best placed to identify that instrument mints a second one beside it.
func TestProposeCandidates_HintedKeyFallsBackToTheDescription(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").Return("", nil)
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), "SRC", "APPLE INC").Return("desc-only-id", nil)

	txs := []*apiv1.Tx{hintedTx("APPLE INC", &apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0378331005"})}
	txHints := [][]identifier.Identifier{{{Type: "ISIN", Value: "US0378331005"}}}

	// No candidate registry, so the key reaches no plugin and the gates below the
	// lookup are not what this is about.
	pre, err := proposeCandidates(context.Background(), ingestDeps{DB: database}, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", txHints[0])
	if pre.descOnly[key] != "desc-only-id" {
		t.Errorf("pre.descOnly[key] = %q, want desc-only-id", pre.descOnly[key])
	}
	// It is a binding, not a resolution. The identifiers still go to the plugins:
	// what the description names has no identity, and finding one is the point.
	if pre.resolved[key].InstrumentID != "" {
		t.Errorf("pre.resolved[key] = %+v, want the key left unresolved", pre.resolved[key])
	}
	if pre.outcome[key] == db.TelemetryCandidateNotAttemptedDBHit {
		t.Error("a description-only hit was recorded as a DB hit; it answers no identifier question")
	}
}

// A key whose identifiers resolve to more than one instrument is recorded rather
// than raised: one bad key must not stop the lookups for the rest of the batch,
// and Resolve raises it at the row that carries it.
func TestProposeCandidates_ConflictIsRecordedNotRaised(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0000000001").Return("inst-a", nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "CUSIP", "", "000000001").Return("inst-b", nil)
	// The second key is still looked up, which is the point.
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), "SRC", "OTHER CO").Return("other-id", nil)

	txs := []*apiv1.Tx{
		hintedTx("AMBIGUOUS",
			&apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0000000001"},
			&apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_CUSIP, Value: "000000001"}),
		tx("OTHER CO"),
	}
	txHints := [][]identifier.Identifier{
		{{Type: "ISIN", Value: "US0000000001"}, {Type: "CUSIP", Value: "000000001"}},
		nil,
	}

	pre, err := proposeCandidates(context.Background(), ingestDeps{DB: database}, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	if !pre.conflicts[cacheKeyWithHints("SRC", "AMBIGUOUS", txHints[0])] {
		t.Error("expected the ambiguous key to be recorded as conflicting")
	}
	if pre.resolved[cacheKeyWithHints("SRC", "OTHER CO", nil)].InstrumentID != "other-id" {
		t.Error("the key after the conflict was not looked up")
	}
}

// A description already bound to an instrument is not paid for. The plugin
// registry is nil, so reaching a plugin would panic rather than pass quietly.
func TestProposeCandidates_ResolvedDescriptionIsNotProposedFor(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), "SRC", "APPLE INC").Return("aapl-id", nil)

	txs := []*apiv1.Tx{tx("APPLE INC")}
	txHints := [][]identifier.Identifier{nil}

	pre, err := proposeCandidates(context.Background(), ingestDeps{DB: database}, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", nil)
	if pre.resolved[key].InstrumentID != "aapl-id" {
		t.Errorf("InstrumentID = %q, want aapl-id", pre.resolved[key].InstrumentID)
	}
	if pre.resolved[key].DBHitOutcome != db.TelemetryResolutionDBSourceDescription {
		t.Errorf("DBHitOutcome = %q, want %q", pre.resolved[key].DBHitOutcome, db.TelemetryResolutionDBSourceDescription)
	}
	if pre.outcome[key] != db.TelemetryCandidateNotAttemptedDBHit {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateNotAttemptedDBHit)
	}
	if len(pre.proposed) != 0 {
		t.Errorf("proposed = %v, want nothing: the description is already resolved", pre.proposed)
	}
}

// candidateRegistry registers one candidate plugin for securities under the id
// the tests below expect ListEnabledPluginConfigs to name.
func candidateRegistry(p *fakeDescPlugin) *candpkg.Registry {
	r := candpkg.NewRegistry()
	r.Register("fake", p)
	return r
}

// expectCandidateConfig makes the one registered candidate plugin enabled.
func expectCandidateConfig(database *mock.MockDB) {
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCandidate).
		Return([]db.PluginConfigRow{{PluginID: "fake", Precedence: 1}}, nil)
}

// A source that stated an ISIN and no venue has left the choice among that
// security's listings open, which is what the candidate stage is for. The stage
// used to be skipped for any posting that stated anything at all, so the QFX
// case -- a CUSIP or ISIN, a currency and no exchange -- never reached it.
func TestProposeCandidates_AStatedIsinWithNoVenueIsCompleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").Return("", nil)
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), "SRC", "APPLE INC").Return("", nil)
	database.EXPECT().ValidateMIC(gomock.Any(), "XNAS").Return(true, nil)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), "XNAS").Return("XNAS", nil)
	expectCandidateConfig(database)

	plugin := &fakeDescPlugin{
		acceptable: map[string]bool{identifier.SecurityTypeHintStock: true},
		resultsByDesc: map[string][]identifier.Identifier{
			"APPLE INC": {{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
		},
	}

	txs := []*apiv1.Tx{hintedTx("APPLE INC", &apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0378331005"})}
	txHints := [][]identifier.Identifier{{{Type: "ISIN", Value: "US0378331005"}}}

	deps := ingestDeps{DB: database, CandidateRegistry: candidateRegistry(plugin), RunKind: db.TelemetryRunTxImport}
	pre, err := proposeCandidates(context.Background(), deps, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", txHints[0])
	got := pre.proposed[key].Proposals
	if len(got) != 1 || got[0].Identifier.Domain != "XNAS" || got[0].Identifier.Value != "AAPL" {
		t.Fatalf("proposed = %v, want one MIC_TICKER XNAS:AAPL", got)
	}
	// The field and the plugin's confidence travel with the value: telemetry
	// records all three, and a filtered slice of identifiers would have lost two.
	if got[0].Field != candpkg.FieldTicker {
		t.Errorf("Field = %q, want %q", got[0].Field, candpkg.FieldTicker)
	}
	if pre.outcome[key] != db.TelemetryCandidateFieldsProposed {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateFieldsProposed)
	}
	// The plugin is asked to complete an identity, so it has to be shown the part
	// of it the source did state.
	if len(plugin.seen) != 1 || len(plugin.seen[0].Stated) != 1 || plugin.seen[0].Stated[0].Value != "US0378331005" {
		t.Errorf("plugin saw Stated = %v, want the stated ISIN", plugin.seen)
	}
}

// A source that named a venue has said the last thing that changes which listing
// resolution lands on, so nothing is asked of a plugin. Reaching one would need
// the plugin config, which is not expected here.
func TestProposeCandidates_AStatedVenueIsNotCompleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("", nil)
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), "SRC", "APPLE INC").Return("", nil)

	plugin := &fakeDescPlugin{
		acceptable:    map[string]bool{identifier.SecurityTypeHintStock: true},
		resultsByDesc: map[string][]identifier.Identifier{"APPLE INC": {{Type: "CURRENCY", Value: "USD"}}},
	}

	txs := []*apiv1.Tx{hintedTx("APPLE INC", &apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_MIC_TICKER, Domain: "XNAS", Value: "AAPL"})}
	txHints := [][]identifier.Identifier{{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}}

	deps := ingestDeps{DB: database, CandidateRegistry: candidateRegistry(plugin), RunKind: db.TelemetryRunTxImport}
	pre, err := proposeCandidates(context.Background(), deps, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", txHints[0])
	if len(plugin.seen) != 0 {
		t.Errorf("the plugin was called for a complete identity: %v", plugin.seen)
	}
	if len(pre.proposed[key].Proposals) != 0 {
		t.Errorf("proposed = %v, want nothing", pre.proposed[key].Proposals)
	}
	if pre.outcome[key] != db.TelemetryCandidateNotAttemptedIdentityComplete {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateNotAttemptedIdentityComplete)
	}
}

// An archive states one identifier per posting, chosen out of an identity the
// exporting instance had already resolved. It is a pointer to that instrument
// rather than a partial description of it, so it is never completed -- the same
// ISIN that reaches a plugin on a broker upload does not reach one here.
func TestProposeCandidates_AnArchiveDoesNotCompleteAPartialIdentity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").Return("", nil)
	database.EXPECT().FindDescriptionOnlyInstrument(gomock.Any(), "SRC", "APPLE INC").Return("", nil)

	plugin := &fakeDescPlugin{
		acceptable:    map[string]bool{identifier.SecurityTypeHintStock: true},
		resultsByDesc: map[string][]identifier.Identifier{"APPLE INC": {{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}},
	}

	txs := []*apiv1.Tx{hintedTx("APPLE INC", &apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0378331005"})}
	txHints := [][]identifier.Identifier{{{Type: "ISIN", Value: "US0378331005"}}}

	deps := ingestDeps{DB: database, CandidateRegistry: candidateRegistry(plugin), RunKind: db.TelemetryRunUserArchiveImport}
	pre, err := proposeCandidates(context.Background(), deps, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	if len(plugin.seen) != 0 {
		t.Errorf("an archive import reached a candidate plugin: %v", plugin.seen)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", txHints[0])
	if pre.outcome[key] != db.TelemetryCandidateNotAttemptedRunKind {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateNotAttemptedRunKind)
	}
}

// An archive posting the exporting instance never resolved names no identifier,
// and it reaches the candidate plugins on its description exactly as it always
// has. Excluding completion must not exclude the stage.
func TestProposeCandidates_AnArchiveStillProposesForADescriptionAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), "SRC", "APPLE INC").Return("", nil)
	database.EXPECT().ValidateMIC(gomock.Any(), "XNAS").Return(true, nil)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), "XNAS").Return("XNAS", nil)
	expectCandidateConfig(database)

	plugin := &fakeDescPlugin{
		acceptable:    map[string]bool{identifier.SecurityTypeHintStock: true},
		resultsByDesc: map[string][]identifier.Identifier{"APPLE INC": {{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}},
	}

	txs := []*apiv1.Tx{tx("APPLE INC")}
	txHints := [][]identifier.Identifier{nil}

	deps := ingestDeps{DB: database, CandidateRegistry: candidateRegistry(plugin), RunKind: db.TelemetryRunUserArchiveImport}
	pre, err := proposeCandidates(context.Background(), deps, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "APPLE INC", nil)
	if len(pre.proposed[key].Proposals) != 1 {
		t.Errorf("proposed = %v, want the description to have been proposed for", pre.proposed[key].Proposals)
	}
}

// A key whose stated identifiers name two instruments is about to fail at the
// row that carries it, so it is not offered completion: the proposal would be
// paid for and thrown away.
func TestProposeCandidates_AConflictIsNotCompleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0000000001").Return("inst-a", nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "CUSIP", "", "000000001").Return("inst-b", nil)

	plugin := &fakeDescPlugin{
		acceptable:    map[string]bool{identifier.SecurityTypeHintStock: true},
		resultsByDesc: map[string][]identifier.Identifier{"AMBIGUOUS": {{Type: "MIC_TICKER", Value: "AMB"}}},
	}

	txs := []*apiv1.Tx{hintedTx("AMBIGUOUS",
		&apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_ISIN, Value: "US0000000001"},
		&apiv1.InstrumentIdentifier{Type: typev1.IdentifierType_CUSIP, Value: "000000001"})}
	txHints := [][]identifier.Identifier{{
		{Type: "ISIN", Value: "US0000000001"},
		{Type: "CUSIP", Value: "000000001"},
	}}

	deps := ingestDeps{DB: database, CandidateRegistry: candidateRegistry(plugin), RunKind: db.TelemetryRunTxImport}
	pre, err := proposeCandidates(context.Background(), deps, "SRC", "IBKR", txs, txHints)
	if err != nil {
		t.Fatalf("proposeCandidates: %v", err)
	}
	key := cacheKeyWithHints("SRC", "AMBIGUOUS", txHints[0])
	if !pre.conflicts[key] {
		t.Fatal("expected the ambiguous key to be recorded as conflicting")
	}
	if len(plugin.seen) != 0 {
		t.Errorf("a conflicting key reached a candidate plugin: %v", plugin.seen)
	}
	if pre.outcome[key] != db.TelemetryCandidateNotAttemptedConflictingHints {
		t.Errorf("outcome = %q, want %q", pre.outcome[key], db.TelemetryCandidateNotAttemptedConflictingHints)
	}
}
