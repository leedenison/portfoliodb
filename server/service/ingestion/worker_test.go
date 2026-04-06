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

func TestExtractDescHints_SameDescDifferentKinds_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	ctx := context.Background()
	source := "Fidelity:web:standard"
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.Now(), InstrumentDescription: "EQQQ ETF", Type: apiv1.TxType_BUYSTOCK, Quantity: 10},
		{Timestamp: timestamppb.Now(), InstrumentDescription: "EQQQ ETF", Type: apiv1.TxType_INCOME, Quantity: 5, TradingCurrency: "GBP"},
	}

	// Only the first tx triggers a DB lookup; the second triggers the kind contradiction.
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), source, "EQQQ ETF").
		Return("", nil)

	_, _, _, valErrs, err := extractDescHints(ctx, database, nil, nil, source, "Fidelity", txs)
	if err != nil {
		t.Fatalf("extractDescHints: %v", err)
	}
	if len(valErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(valErrs))
	}
	if valErrs[0].RowIndex != 1 {
		t.Errorf("RowIndex = %d, want 1", valErrs[0].RowIndex)
	}
}

func TestExtractDescHints_DBKindContradiction_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	ctx := context.Background()
	source := "Fidelity:web:standard"
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.Now(), InstrumentDescription: "EQQQ ETF", Type: apiv1.TxType_INCOME, Quantity: 5, TradingCurrency: "GBP"},
	}
	acETF := "ETF"
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), source, "EQQQ ETF").
		Return("inst-1", nil)
	database.EXPECT().
		GetInstrument(gomock.Any(), "inst-1").
		Return(&db.InstrumentRow{ID: "inst-1", AssetClass: &acETF}, nil)

	_, _, _, valErrs, err := extractDescHints(ctx, database, nil, nil, source, "Fidelity", txs)
	if err != nil {
		t.Fatalf("extractDescHints: %v", err)
	}
	if len(valErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(valErrs))
	}
	if valErrs[0].RowIndex != 0 {
		t.Errorf("RowIndex = %d, want 0", valErrs[0].RowIndex)
	}
}

func TestExtractDescHints_DBUnidentified_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	ctx := context.Background()
	source := "Fidelity:web:standard"
	txs := []*apiv1.Tx{
		{Timestamp: timestamppb.Now(), InstrumentDescription: "MYSTERY", Type: apiv1.TxType_INCOME, Quantity: 5, TradingCurrency: "GBP"},
	}
	// Instrument exists but has nil asset class (unidentified).
	database.EXPECT().
		FindInstrumentBySourceDescription(gomock.Any(), source, "MYSTERY").
		Return("inst-1", nil)
	database.EXPECT().
		GetInstrument(gomock.Any(), "inst-1").
		Return(&db.InstrumentRow{ID: "inst-1"}, nil)

	_, _, _, valErrs, err := extractDescHints(ctx, database, nil, nil, source, "Fidelity", txs)
	if err != nil {
		t.Fatalf("extractDescHints: %v", err)
	}
	if len(valErrs) != 0 {
		t.Errorf("expected no validation errors, got %d: %+v", len(valErrs), valErrs)
	}
}

func TestResolveInstruments_IdentifierContradiction_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "Fidelity:web:standard"
	desc := "APPLE INC"
	txs := []*apiv1.Tx{
		{
			Timestamp:             timestamppb.Now(),
			InstrumentDescription: desc,
			Type:                  apiv1.TxType_BUYSTOCK,
			Quantity:              10,
			IdentifierHints: []*apiv1.InstrumentIdentifier{
				{Type: apiv1.IdentifierType_ISIN, Value: "US9999999999"},
			},
		},
	}
	cache := map[string]resolveResult{
		cacheKey(source, desc): {InstrumentID: "inst-1"},
	}
	descInstruments := map[string]*db.InstrumentRow{
		cacheKey(source, desc): {
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "ISIN", Value: "US0378331005"}},
		},
	}

	_, _, valErrs, err := resolveInstruments(ctx, database, registry, "Fidelity", source, "job-1", nil, txs, []int{0}, cache, nil, descInstruments)
	if err != nil {
		t.Fatalf("resolveInstruments: %v", err)
	}
	if len(valErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(valErrs))
	}
	if valErrs[0].RowIndex != 0 {
		t.Errorf("RowIndex = %d, want 0", valErrs[0].RowIndex)
	}
}

func TestResolveInstruments_IdentifierMatch_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "Fidelity:web:standard"
	desc := "APPLE INC"
	txs := []*apiv1.Tx{
		{
			Timestamp:             timestamppb.Now(),
			InstrumentDescription: desc,
			Type:                  apiv1.TxType_BUYSTOCK,
			Quantity:              10,
			IdentifierHints: []*apiv1.InstrumentIdentifier{
				{Type: apiv1.IdentifierType_ISIN, Value: "US0378331005"},
			},
		},
	}
	cache := map[string]resolveResult{
		cacheKey(source, desc): {InstrumentID: "inst-1"},
	}
	descInstruments := map[string]*db.InstrumentRow{
		cacheKey(source, desc): {
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "ISIN", Value: "US0378331005"}},
		},
	}

	// Path A: Resolve looks up by identifier hint.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("inst-1", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-1").Return(nil)

	ids, _, valErrs, err := resolveInstruments(ctx, database, registry, "Fidelity", source, "job-1", nil, txs, []int{0}, cache, nil, descInstruments)
	if err != nil {
		t.Fatalf("resolveInstruments: %v", err)
	}
	if len(valErrs) != 0 {
		t.Errorf("expected no validation errors, got %d: %+v", len(valErrs), valErrs)
	}
	if ids[0] != "inst-1" {
		t.Errorf("instrumentID = %q, want inst-1", ids[0])
	}
}

func TestResolveInstruments_NewIdentifierType_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "Fidelity:web:standard"
	desc := "APPLE INC"
	txs := []*apiv1.Tx{
		{
			Timestamp:             timestamppb.Now(),
			InstrumentDescription: desc,
			Type:                  apiv1.TxType_BUYSTOCK,
			Quantity:              10,
			IdentifierHints: []*apiv1.InstrumentIdentifier{
				{Type: apiv1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
			},
		},
	}
	cache := map[string]resolveResult{
		cacheKey(source, desc): {InstrumentID: "inst-1"},
	}
	// Instrument has ISIN but tx supplies MIC_TICKER — different type, no contradiction.
	descInstruments := map[string]*db.InstrumentRow{
		cacheKey(source, desc): {
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "ISIN", Value: "US0378331005"}},
		},
	}

	// Path A: Resolve looks up by identifier hint.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("inst-1", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-1").Return(nil)

	ids, _, valErrs, err := resolveInstruments(ctx, database, registry, "Fidelity", source, "job-1", nil, txs, []int{0}, cache, nil, descInstruments)
	if err != nil {
		t.Fatalf("resolveInstruments: %v", err)
	}
	if len(valErrs) != 0 {
		t.Errorf("expected no validation errors, got %d: %+v", len(valErrs), valErrs)
	}
	if ids[0] != "inst-1" {
		t.Errorf("instrumentID = %q, want inst-1", ids[0])
	}
}
