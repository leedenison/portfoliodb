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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
	// Resolve for "UNKNOWN": DB miss, nil descRegistry -> extraction failed, EnsureInstrument broker-only
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "UNKNOWN", Canonical: false}}, "", nil, nil, nil).
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
		EnsureInstrument(gomock.Any(), "", "", "", "CACHED", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: "IBKR:test:statement", Value: "CACHED", Canonical: false}}, "", nil, nil, nil).
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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

// TestProcessBulk_StockEtfEquivalence verifies that a stated STOCK resolved to
// an ETF instrument is accepted as compatible (broker-level equivalence).
func TestProcessBulk_StockEtfEquivalence(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
// UNKNOWN -- a security of unstated class -- resolved to a CASH instrument is
// rejected.
func TestProcessBulk_TransferToCashRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "USD CASH", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, AssetClassHint: typev1.AssetClass_UNKNOWN, Quantity: "10", Account: ""},
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
// the vintage an OCC hint is rebased from is the upload's own exported_at and not
// each posting's trade date. The observable is which symbol the lookup asks for:
// an export after the ex_date already carries the restated strike and must be
// looked up as it stands, while one before it is carried forward to today.
func TestProcessTx_RebasesOCCFromTheUploadVintageNotTheTradeDate(t *testing.T) {
	const preSplit = "AAPL250117C00760000"
	const postSplit = "AAPL250117C00190000"
	exDate := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	tradeDate := timestamppb.New(time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))

	cases := []struct {
		name       string
		exportedAt *timestamppb.Timestamp
		wantLookup string
	}{{
		name: "export after the ex_date states the restated symbol",
		// The trade predates the split and the export does not, which is the
		// ordinary shape of a broker file covering a split. Rebasing here would
		// halve a strike the file had already halved.
		exportedAt: timestamppb.New(time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)),
		wantLookup: preSplit,
	}, {
		name:       "export before the ex_date is carried forward",
		exportedAt: timestamppb.New(time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)),
		wantLookup: postSplit,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			database := mock.NewMockDB(ctrl)
			database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
			database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			registry := identifier.NewRegistry() // no plugins

			payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
				ExportedAt: tc.exportedAt,
				Window: &archivev1.TxWindow{
					Broker:       typev1.Broker_IBKR,
					Source:       "IBKR:test:statement",
					PeriodFrom:   tradeDate,
					PeriodBefore: timestamppb.New(exDate.AddDate(1, 0, 0)),
					Postings: []*archivev1.Posting{{
						OrderDate:             tradeDate,
						TradeDate:             tradeDate,
						InstrumentDescription: "AAPL 250117C00760000",
						BrokerTxType:          []typev1.TxType{typev1.TxType_TRADE_ASSET},
						Quantity:              "1",
						IdentifierHints: []*archivev1.InstrumentRef{
							{Type: typev1.IdentifierType_OCC, Value: preSplit},
						},
					}},
				},
			})

			database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_RUNNING).Return(nil)
			expectLoadPayload(database, "job-1", "user-1", payload)
			database.EXPECT().SetJobTotalCount(gomock.Any(), "job-1", int32(1)).Return(nil)
			database.EXPECT().FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "AAPL 250117C00760000").Return("", nil)
			database.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "AAPL").
				Return([]db.StockSplit{{ExDate: exDate, SplitFrom: "1", SplitTo: "4"}}, nil).AnyTimes()

			// The hint as stated, looked up before any rebasing can happen.
			database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "OCC", "", preSplit).Return("", nil)
			// The hint as resolution decided it should be spelled: the assertion.
			var looked []string
			database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", gomock.Any()).
				DoAndReturn(func(_ context.Context, _, _, value string) (string, string, string, string, error) {
					looked = append(looked, value)
					return "", "", "", "", nil
				})
			database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "OCC", gomock.Any()).Return("", nil).AnyTimes()
			database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).Return(nil, nil)
			database.EXPECT().EnsureInstrument(gomock.Any(), "", "", "", "AAPL 250117C00760000", "", "", gomock.Any(), "", nil, nil, nil).Return("broker-only-id", nil)
			database.EXPECT().AppendIdentificationErrors(gomock.Any(), "job-1", gomock.Any()).Return(nil).AnyTimes()
			database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-1").Return(nil)
			database.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{"broker-only-id"}).
				Return([]*db.InstrumentRow{{ID: "broker-only-id"}}, nil)
			database.EXPECT().ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			database.EXPECT().InstrumentsWithSplits(gomock.Any(), gomock.Any()).Return(nil, nil)
			database.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
			database.EXPECT().SetJobStatus(gomock.Any(), "job-1", apiv1.JobStatus_SUCCESS).Return(nil)

			processJob(context.Background(), WorkerOptions{DB: database, IdentifierRegistry: registry}, &JobRequest{JobID: "job-1", JobType: "tx"})

			if len(looked) != 1 || looked[0] != tc.wantLookup {
				t.Errorf("OCC looked up = %v, want [%s]", looked, tc.wantLookup)
			}
		})
	}
}
