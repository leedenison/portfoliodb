package ingestion

import (
	"context"
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
	// (type, domain, value). With asset_class set and no plugins registered,
	// resolveOrIdentifyInstrument falls back to ensureWithSuppliedIdentifier.
	// The path is "asset class set + plugins registry empty" -- the registry
	// in this test is empty so the plugin path is skipped and we hit the
	// DB-only lookup branch.
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", nil)

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
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-1", apiv1.JobStatus_SUCCESS).Return(nil)

	processCorporateEventImport(context.Background(), database, registry, j)
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

	processCorporateEventImport(context.Background(), database, registry, j)

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "split_from/split_to" {
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
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-ce-3").Return(nil)
	database.EXPECT().UpsertCashDividends(gomock.Any(), gomock.Any()).Return(nil)
	// Critically: NO RecomputeSplitAdjustments call.
	database.EXPECT().SetJobStatus(gomock.Any(), "job-ce-3", apiv1.JobStatus_SUCCESS).Return(nil)

	processCorporateEventImport(context.Background(), database, registry, j)
}
