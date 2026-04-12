package ingestion

import (
	"context"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
)

// TestProcessCorporateEventImport_HappyPath verifies that a mixed split +
// dividend import resolves the instrument once (cached), upserts both event
// types, writes the coverage row, calls RecomputeSplitAdjustments because a
// split landed, and marks the job SUCCESS.
func TestProcessCorporateEventImport_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Split{Split: &apiv1.SplitRow{
					ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "4",
				}},
			},
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Dividend{Dividend: &apiv1.CashDividendRow{
					ExDate:    "2024-02-09",
					PayDate:   "2024-02-15",
					Amount:    "0.24",
					Currency:  "USD",
					Frequency: "quarterly",
				}},
			},
		},
		Coverage: []*apiv1.ImportCorporateEventCoverage{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				From:             "2014-01-01",
				To:               "2024-12-31",
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-ce-1", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-1").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-1").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-1", int32(2)).Return(nil)

	// Resolution: only one cache miss because both events share the same
	// (type, domain, value). The plugin path's fast DB lookup resolves
	// the instrument; GetInstrument is called to compare hints.
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-aapl").
		Return(stubInstrument("inst-aapl", "STOCK", "XNAS", "USD"), nil)

	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-1").Return(nil).Times(2)

	database.EXPECT().UpsertStockSplits(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, splits []db.StockSplit) error {
			if len(splits) != 1 {
				t.Errorf("expected 1 split, got %d", len(splits))
			}
			if splits[0].InstrumentID != "inst-aapl" {
				t.Errorf("instrument: %s", splits[0].InstrumentID)
			}
			if splits[0].SplitFrom != "1" || splits[0].SplitTo != "4" {
				t.Errorf("ratio: %+v", splits[0])
			}
			if splits[0].DataProvider != db.CorporateEventProviderImport {
				t.Errorf("provider: %s", splits[0].DataProvider)
			}
			return nil
		})
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, divs []db.CashDividend) error {
			if len(divs) != 1 {
				t.Errorf("expected 1 dividend, got %d", len(divs))
			}
			if divs[0].Amount != "0.24" || divs[0].Currency != "USD" {
				t.Errorf("dividend: %+v", divs[0])
			}
			if divs[0].PayDate == nil || !divs[0].PayDate.Equal(time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("pay date: %+v", divs[0].PayDate)
			}
			return nil
		})
	database.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), "inst-aapl", db.CorporateEventProviderImport,
		time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)).Return(nil)
	database.EXPECT().RecomputeSplitAdjustments(gomock.Any(), "inst-aapl").Return(nil)
	database.EXPECT().ListStockSplits(gomock.Any(), "inst-aapl").Return([]db.StockSplit{
		{InstrumentID: "inst-aapl", ExDate: time.Date(2020, 8, 31, 0, 0, 0, 0, time.UTC), SplitFrom: "1", SplitTo: "4", DataProvider: "import"},
	}, nil)
	database.EXPECT().ListOptionsByUnderlying(gomock.Any(), "inst-aapl").Return(nil, nil)
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-1", apiv1.JobStatus_SUCCESS).Return(nil)

	if !processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessCorporateEventImport_RejectsBadSplitRatio verifies that a row
// with split_to = 0 is reported as a validation error and does not reach the
// upsert path.
func TestProcessCorporateEventImport_RejectsBadSplitRatio(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Split{Split: &apiv1.SplitRow{
					ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "0",
				}},
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-2", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-2").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-2").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-2", int32(1)).Return(nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("inst-aapl", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-aapl").
		Return(stubInstrument("inst-aapl", "STOCK", "XNAS", "USD"), nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-2").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().AppendValidationErrors(gomock.Any(), "job-ce-2", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	// No upserts and no recompute (no valid splits landed). Coverage is
	// still attempted but the request has none.
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-2", apiv1.JobStatus_SUCCESS).Return(nil)

	if processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=false when every row was rejected")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "split_to" {
		t.Errorf("field: %s", capturedErrs[0].Field)
	}
}

// TestProcessCorporateEventImport_DividendOnlyDoesNotRecompute verifies that a
// dividend-only import does NOT call RecomputeSplitAdjustments.
func TestProcessCorporateEventImport_DividendOnlyDoesNotRecompute(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "MSFT",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Dividend{Dividend: &apiv1.CashDividendRow{
					ExDate: "2024-02-15", Amount: "0.75", Currency: "USD",
				}},
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-3", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-3").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-3").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-3", int32(1)).Return(nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-msft").
		Return(stubInstrument("inst-msft", "STOCK", "XNAS", "USD"), nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-3").Return(nil)
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).Return(nil)
	// Critically: NO RecomputeSplitAdjustments call.
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-3", apiv1.JobStatus_SUCCESS).Return(nil)

	if !processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=true after a successful dividend upsert")
	}
}

// TestProcessCorporateEventImport_RejectsBadCoverageDate verifies that a
// coverage row with an invalid from-date is recorded as a validation error
// and does not silently disappear. The job still SUCCEEDs (the events that
// did parse are upserted) but the validation error surfaces the partial
// failure.
func TestProcessCorporateEventImport_RejectsBadCoverageDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Coverage: []*apiv1.ImportCorporateEventCoverage{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				From:             "2024-13-01", // invalid month
				To:               "2024-12-31",
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-cov", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-cov").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-cov").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-cov", int32(0)).Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().AppendValidationErrors(gomock.Any(), "job-ce-cov", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-cov", apiv1.JobStatus_SUCCESS).Return(nil)

	if processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=false when no events or coverage rows succeeded")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "coverage.from" {
		t.Errorf("field: got %q, want coverage.from", capturedErrs[0].Field)
	}
	if capturedErrs[0].RowIndex != -1 {
		t.Errorf("row index: got %d, want -1", capturedErrs[0].RowIndex)
	}
}

// TestProcessCorporateEventImport_AcceptsHighPrecisionDecimal verifies that
// the parseDecimal helper accepts values that have no exact float64
// representation (e.g. "0.1") -- the previous strconv.ParseFloat-based
// validator would silently round-trip these.
func TestProcessCorporateEventImport_AcceptsHighPrecisionDecimal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "MSFT",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Dividend{Dividend: &apiv1.CashDividendRow{
					ExDate: "2024-02-15", Amount: "0.1", Currency: "USD",
				}},
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-prec", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-prec").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-prec").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-prec", int32(1)).Return(nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-msft").
		Return(stubInstrument("inst-msft", "STOCK", "XNAS", "USD"), nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-prec").Return(nil)
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, divs []db.CashDividend) error {
			if divs[0].Amount != "0.1" {
				t.Errorf("expected raw string 0.1 stored, got %q", divs[0].Amount)
			}
			return nil
		})
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-prec", apiv1.JobStatus_SUCCESS).Return(nil)

	if !processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=true")
	}
}

// TestProcessCorporateEventImport_RejectsInvalidDecimal verifies that a
// non-numeric amount is reported as a validation error.
func TestProcessCorporateEventImport_RejectsInvalidDecimal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "MSFT",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Dividend{Dividend: &apiv1.CashDividendRow{
					ExDate: "2024-02-15", Amount: "abc", Currency: "USD",
				}},
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-bad", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-bad").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-bad").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-bad", int32(1)).Return(nil)
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "MSFT").Return("inst-msft", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-msft").
		Return(stubInstrument("inst-msft", "STOCK", "XNAS", "USD"), nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-bad").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().AppendValidationErrors(gomock.Any(), "job-ce-bad", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-bad", apiv1.JobStatus_SUCCESS).Return(nil)

	if processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=false")
	}
	if len(capturedErrs) != 1 || capturedErrs[0].Field != "amount" {
		t.Fatalf("expected one validation error on field=amount, got %+v", capturedErrs)
	}
}

// TestProcessCorporateEventImport_RejectsHintDiff verifies that when the
// resolved instrument's asset class differs from the import row's declared
// asset class, the row is rejected with a validation error.
func TestProcessCorporateEventImport_RejectsHintDiff(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	req := &apiv1.ImportCorporateEventsRequest{
		Events: []*apiv1.ImportCorporateEventRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierDomain: "XNAS",
				IdentifierValue:  "AAPL",
				AssetClass:       apiv1.AssetClass_ASSET_CLASS_STOCK,
				Event: &apiv1.ImportCorporateEventRow_Split{Split: &apiv1.SplitRow{
					ExDate: "2020-08-31", SplitFrom: "1", SplitTo: "4",
				}},
			},
		},
	}
	payload, _ := proto.Marshal(req)
	j := &JobRequest{JobID: "job-ce-diff", JobType: db.JobTypeCorporateEvent}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-ce-diff").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-ce-diff").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-ce-diff", int32(1)).Return(nil)

	// Instrument found but has asset class ETF, not STOCK.
	database.EXPECT().FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").Return("inst-aapl", nil)
	database.EXPECT().GetInstrument(gomock.Any(), "inst-aapl").
		Return(stubInstrument("inst-aapl", "ETF", "XNAS", "USD"), nil) // asset class mismatch
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-diff").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().AppendValidationErrors(gomock.Any(), "job-ce-diff", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-diff", apiv1.JobStatus_SUCCESS).Return(nil)

	if processCorporateEventImport(context.Background(), database, registry, j) {
		t.Error("expected persisted=false when row was rejected for hint diff")
	}
	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "identifier" {
		t.Errorf("expected field=identifier, got %s", capturedErrs[0].Field)
	}
	if !strings.Contains(capturedErrs[0].Message, "SecurityType") {
		t.Errorf("expected message to mention SecurityType, got %s", capturedErrs[0].Message)
	}
}
