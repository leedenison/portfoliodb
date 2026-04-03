package ingestion

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
)

func TestProcessPriceImport_RejectsUnknownIdentifierType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	req := &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{
			{
				IdentifierType:  "TICKER",
				IdentifierValue: "AAPL",
				PriceDate:       "2024-01-15",
				Close:           185.90,
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-price-1", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-1").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-1").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-1", int32(1)).Return(nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-1").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-price-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-1", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processPriceImport(ctx, database, registry, j)

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "identifier_type" {
		t.Errorf("expected field=identifier_type, got %s", capturedErrs[0].Field)
	}
	if capturedErrs[0].RowIndex != 0 {
		t.Errorf("expected row_index=0, got %d", capturedErrs[0].RowIndex)
	}
}

func TestProcessPriceImport_AcceptsValidIdentifierType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	req := &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierValue:  "AAPL",
				IdentifierDomain: "XNAS",
				PriceDate:        "2024-01-15",
				Close:            185.90,
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-price-2", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-2").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-2").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-2", int32(1)).Return(nil)

	// Valid type passes validation, so resolveOrIdentifyInstrument is called.
	// With no asset_class and no plugins, it does DB-only lookup.
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-2").Return(nil)
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processPriceImport(ctx, database, registry, j)
}

func TestProcessPriceImport_WithCoverage_UsesUpsertWithFill(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	req := &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierValue:  "AAPL",
				IdentifierDomain: "XNAS",
				PriceDate:        "2024-01-15",
				Close:            185.90,
			},
		},
		Coverage: []*apiv1.ImportCoverage{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierValue:  "AAPL",
				IdentifierDomain: "XNAS",
				From:             "2024-01-01",
				To:               "2024-04-01",
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-price-cov", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-cov").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-cov").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-cov", int32(1)).Return(nil)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-cov").Return(nil)
	// Expect UpsertPricesWithFill (not UpsertPrices) because coverage was provided.
	database.EXPECT().
		UpsertPricesWithFill(gomock.Any(), "inst-aapl", "import", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-cov", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processPriceImport(ctx, database, registry, j)
}

func TestProcessPriceImport_WithCoverage_NoCoverageForInstrument_UsesPlanUpsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	req := &apiv1.ImportPricesRequest{
		Prices: []*apiv1.ImportPriceRow{
			{
				IdentifierType:   "MIC_TICKER",
				IdentifierValue:  "AAPL",
				IdentifierDomain: "XNAS",
				PriceDate:        "2024-01-15",
				Close:            185.90,
			},
		},
		Coverage: []*apiv1.ImportCoverage{
			{
				// Coverage for a different instrument.
				IdentifierType:   "FX_PAIR",
				IdentifierValue:  "GBPUSD",
				From:             "2024-01-01",
				To:               "2024-04-01",
			},
		},
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	j := &JobRequest{JobID: "job-price-nocov", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-nocov").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-nocov").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-nocov", int32(1)).Return(nil)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-nocov").Return(nil)
	// No coverage match for AAPL, so expect plain UpsertPrices.
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-nocov", apiv1.JobStatus_SUCCESS).
		Return(nil)

	processPriceImport(ctx, database, registry, j)
}
