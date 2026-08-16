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

// expectLoadPayload sets up LoadJobPayload + ClearJobPayload + GetJob(userID) + ListIgnoredAssetClasses mocks.
func expectLoadPayload(database *mock.MockDB, jobID, userID string, payload []byte) {
	database.EXPECT().LoadJobPayload(gomock.Any(), jobID).Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), jobID).Return(nil)
	database.EXPECT().GetJob(gomock.Any(), jobID).Return(
		&db.JobDetail{Status: apiv1.JobStatus_RUNNING, UserID: userID}, nil,
	)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), userID).Return(nil, nil)
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

func TestProcessBulk_DropsIgnoredTxs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	from := timestamppb.Now()
	before := timestamppb.Now()
	// Both legs carry the record's own reference, which is what the store puts them
	// back together by once they are stored.
	ref := []*archivev1.Correlation{{
		Token: "ref-1", Scope: typev1.Scope_SCOPE_FILE,
		Match: []typev1.Match{typev1.Match_MATCH_EXACT},
	}}
	postings := []*archivev1.Posting{
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10", Account: "", Correlations: ref},
		// The ignore rules match the stated asset class, so the cash journal
		// carries the CASH claim the broker's file made.
		{OrderDate: from,
			TradeDate: from, InstrumentDescription: "GBP", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, AssetClassHint: typev1.AssetClass_CASH, Quantity: "1", Account: "", Correlations: ref},
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
	j := &JobRequest{JobID: "job-ignored", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-ignored", apiv1.JobStatus_RUNNING).
		Return(nil)
	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ignored").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ignored").Return(nil)
	database.EXPECT().GetJob(gomock.Any(), "job-ignored").Return(
		&db.JobDetail{Status: apiv1.JobStatus_RUNNING, UserID: "user-1"}, nil,
	)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(
		[]db.IgnoredAssetClass{{Broker: "IBKR", AssetClass: "CASH"}}, nil,
	)
	database.EXPECT().
		SetJobTotalCount(gomock.Any(), "job-ignored", int32(1)).
		Return(nil)
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), "IBKR:test:statement", "AAPL").
		Return("", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "AAPL", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("aapl-id", nil)
	database.EXPECT().
		IncrJobProcessedCount(gomock.Any(), "job-ignored").
		Return(nil)
	database.EXPECT().
		AppendIdentificationErrors(gomock.Any(), "job-ignored", gomock.Any()).
		Return(nil)
	database.EXPECT().
		ListInstrumentsByIDs(gomock.Any(), []string{"aapl-id"}).
		Return([]*db.InstrumentRow{{ID: "aapl-id"}}, nil)
	database.EXPECT().
		ReplaceTxsInPeriod(gomock.Any(), "user-1", "IBKR", "job-ignored", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _ string, _, _ *timestamppb.Timestamp, storedTxs []*apiv1.Tx, ids []string, ws []db.Weight, _ []*time.Time) error {
			supplied := userPostings(storedTxs)
			if len(supplied) != 1 || supplied[0].InstrumentDescription != "AAPL" || supplied[0].GetResolvedTxType() != typev1.TxType_TRADE_ASSET {
				t.Errorf("ReplaceTxsInPeriod called with %d supplied txs, expected 1 (AAPL TRADE_ASSET)", len(supplied))
			}
			// Dropping a leg must not lose the surviving leg's evidence, which is
			// what the store partitions on once the postings are down.
			if got := supplied[0].GetCorrelations(); len(got) != 1 || got[0].GetToken() != "ref-1" {
				t.Errorf("correlations after dropping a leg: want the row's own token, got %v", got)
			}
			if len(ids) != len(storedTxs) {
				t.Errorf("instrument ids (%d) and txs (%d) must stay parallel", len(ids), len(storedTxs))
			}
			// Weights are appended in two steps -- the supplied postings, then the
			// routed ones -- so a slice that has fallen out of step is the failure
			// mode worth pinning.
			if len(ws) != len(storedTxs) {
				t.Errorf("weights (%d) and txs (%d) must stay parallel", len(ws), len(storedTxs))
			}
			return nil
		})
	database.EXPECT().
		InstrumentsWithSplits(gomock.Any(), gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-ignored", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}

// userPostings returns the postings the upload supplied, dropping the
// counterparties routing added to balance their groups.
func userPostings(txs []*apiv1.Tx) []*apiv1.Tx {
	var out []*apiv1.Tx
	for _, t := range txs {
		if t.GetAccountType() == typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED ||
			t.GetAccountType() == typev1.AccountType_ACCOUNT_TYPE_USER {
			out = append(out, t)
		}
	}
	return out
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

func TestProcessSingle_DropsIgnoredTx(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := marshalPayload(t, &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker: typev1.Broker_IBKR,
			Source: "IBKR:test:statement",
			Postings: []*archivev1.Posting{{OrderDate: timestamppb.Now(),
				TradeDate: timestamppb.Now(), InstrumentDescription: "GBP", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, AssetClassHint: typev1.AssetClass_CASH, Quantity: "1", Account: ""}},
		},
	})
	j := &JobRequest{JobID: "job-single-ignored", JobType: "tx"}

	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-ignored", apiv1.JobStatus_RUNNING).
		Return(nil)
	database.EXPECT().LoadJobPayload(gomock.Any(), "job-single-ignored").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-single-ignored").Return(nil)
	database.EXPECT().GetJob(gomock.Any(), "job-single-ignored").Return(
		&db.JobDetail{Status: apiv1.JobStatus_RUNNING, UserID: "user-1"}, nil,
	)
	database.EXPECT().ListIgnoredAssetClasses(gomock.Any(), "user-1").Return(
		[]db.IgnoredAssetClass{{Broker: "IBKR", AssetClass: "CASH"}}, nil,
	)
	database.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return(nil, nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-single-ignored", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processJob(ctx, WorkerOptions{DB: database, IdentifierRegistry: registry}, j)
}
