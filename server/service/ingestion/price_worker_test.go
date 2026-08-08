package ingestion

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"testing"
	"time"
)

// pricePayload marshals the request a price import job carries. exportedAt is
// nil only to exercise the worker's guard for a payload written before the
// envelope was required.
func pricePayload(t *testing.T, exportedAt *timestamppb.Timestamp, groups ...*archivev1.PriceGroup) []byte {
	t.Helper()
	env := archive.NewEnvelope("test", archivev1.ArchiveKind_ADMIN)
	env.ExportedAt = exportedAt
	payload, err := proto.Marshal(&apiv1.ImportPricesRequest{
		Envelope: env,
		Prices:   &archivev1.PricePart{Groups: groups},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return payload
}

func TestProcessPriceImport_RejectsUnknownIdentifierType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	// A valid enum value the resolver has no plugin vocabulary for.
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OPENFIGI_GLOBAL, Value: "BBG000B9XRY4"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

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

	if processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=false when every row was rejected")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "instrument" {
		t.Errorf("expected field=instrument, got %s", capturedErrs[0].Field)
	}
	if capturedErrs[0].RowIndex != 0 {
		t.Errorf("expected row_index=0, got %d", capturedErrs[0].RowIndex)
	}
}

func TestProcessPriceImport_AcceptsValidIdentifierType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	j := &JobRequest{JobID: "job-price-2", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-2").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-2").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-2", int32(1)).Return(nil)

	// Valid type passes validation, so resolveOrIdentifyInstrument is called.
	// With no asset_class and no plugins, it does DB-only lookup.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-2").Return(nil)
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-2", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// The basis is stated per bar, so one group carries an as-traded stretch and a
// back-adjusted one. A bar that omits it is denominated in its own date, which
// is what the NOT NULL column defaults to.
func TestProcessPriceImport_CarriesShareCountBasis(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "NVDA", Domain: "XNAS"},
		Rows: []*archivev1.PriceRow{
			{PriceDate: "2024-01-15", Close: "48.0"},
			{PriceDate: "2024-01-16", ShareCountBasis: proto.String("2024-06-10"), Close: "4.8"},
		},
	})

	j := &JobRequest{JobID: "job-price-basis", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-basis").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-basis").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-basis", int32(2)).Return(nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "NVDA").
		Return("inst-nvda", "", "XNAS", "", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-basis").Return(nil).Times(2)

	var captured []db.EODPrice
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, prices []db.EODPrice) error {
			captured = prices
			return nil
		})
	database.EXPECT().SetJobStatus(gomock.Any(), "job-price-basis", apiv1.JobStatus_SUCCESS).Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Fatal("expected persisted=true after a successful upsert")
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 bars, got %d", len(captured))
	}
	if captured[0].ShareCountBasis != nil {
		t.Errorf("expected the as-traded bar to carry no basis, got %v", captured[0].ShareCountBasis)
	}
	want := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	if captured[1].ShareCountBasis == nil || !captured[1].ShareCountBasis.Equal(want) {
		t.Errorf("expected basis 2024-06-10, got %v", captured[1].ShareCountBasis)
	}
}

// A span holding no bars is the one thing rows cannot say: the provider was
// asked about those dates and had nothing. It has to reach the coverage table
// even though there is nothing to upsert alongside it.
func TestProcessPriceImport_CoverageWithNoRows(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "DELISTED", Domain: "XNAS"},
		Coverage:   []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-04-01"}},
	})

	j := &JobRequest{JobID: "job-price-empty", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-empty").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-empty").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-empty", int32(0)).Return(nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "DELISTED").
		Return("inst-delisted", "", "XNAS", "", nil)
	database.EXPECT().
		UpsertPricesForRange(gomock.Any(), "inst-delisted", "import", gomock.Len(0),
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), gomock.Any()).
		Return(nil)
	database.EXPECT().SetJobStatus(gomock.Any(), "job-price-empty", apiv1.JobStatus_SUCCESS).Return(nil)

	// No bars were stored, so there is nothing for the price fetcher to react to.
	if processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=false when the group carried no bars")
	}
}

func TestProcessPriceImport_WithCoverage_UsesUpsertWithFill(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Coverage:   []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-04-01"}},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	j := &JobRequest{JobID: "job-price-cov", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-cov").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-cov").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-cov", int32(1)).Return(nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-cov").Return(nil)
	// Expect UpsertPricesForRange (not UpsertPrices) because coverage was provided.
	database.EXPECT().
		UpsertPricesForRange(gomock.Any(), "inst-aapl", "import", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-cov", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

func TestProcessPriceImport_WithCoverage_NoCoverageForInstrument_UsesPlanUpsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	// Coverage now sits inside the group it applies to, so a group that
	// declares none has bars covering only their own dates.
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	j := &JobRequest{JobID: "job-price-nocov", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-nocov").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-nocov").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-nocov", int32(1)).Return(nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-nocov").Return(nil)
	// No coverage match for AAPL, so expect plain UpsertPrices.
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-nocov", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessPriceImport_RejectsHintDiff verifies that when the resolved
// instrument's exchange differs from the import identifier domain, the row
// is rejected with a validation error.
func TestProcessPriceImport_RejectsHintDiff(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	j := &JobRequest{JobID: "job-price-diff", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-diff").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-diff").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-diff", int32(1)).Return(nil)

	// DB-only lookup succeeds, but the instrument has a different exchange.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNYS", "", nil) // exchange mismatch: XNYS != XNAS
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-diff").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-price-diff", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-diff", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=false when row was rejected for hint diff")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if capturedErrs[0].Field != "instrument" {
		t.Errorf("expected field=instrument, got %s", capturedErrs[0].Field)
	}
	if !strings.Contains(capturedErrs[0].Message, "Exchange") {
		t.Errorf("expected message to mention Exchange, got %s", capturedErrs[0].Message)
	}
}

// TestProcessPriceImport_RejectsCurrencyHintDiff verifies that when the
// import row carries a currency that differs from the resolved instrument's
// currency, the row is rejected with a validation error.
func TestProcessPriceImport_RejectsCurrencyHintDiff(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Currency:   "GBP",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	j := &JobRequest{JobID: "job-price-curdiff", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-curdiff").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-curdiff").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-curdiff", int32(1)).Return(nil)

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "USD", nil) // currency mismatch: USD != GBP
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-curdiff").Return(nil)

	var capturedErrs []*apiv1.ValidationError
	database.EXPECT().
		AppendValidationErrors(gomock.Any(), "job-price-curdiff", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, errs []*apiv1.ValidationError) error {
			capturedErrs = errs
			return nil
		})
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-curdiff", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=false when row was rejected for currency hint diff")
	}

	if len(capturedErrs) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(capturedErrs))
	}
	if !strings.Contains(capturedErrs[0].Message, "Currency") {
		t.Errorf("expected message to mention Currency, got %s", capturedErrs[0].Message)
	}
}

// TestProcessPriceImport_FallbackPassesAssetClassAndCurrency verifies that
// when no identifier plugin can handle the identifier type (e.g. FX_PAIR),
// the fallback creates the instrument with the asset class and currency from
// the import row rather than empty strings.
func TestProcessPriceImport_FallbackPassesAssetClassAndCurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_FX_PAIR, Value: "EURGBP"},
		AssetClass: typev1.AssetClass_FX,
		Currency:   "EUR",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2021-12-31", Close: "0.84"}},
	})

	j := &JobRequest{JobID: "job-price-fx", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-fx").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-fx").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-fx", int32(1)).Return(nil)

	// asset_class is non-empty so the plugin path is taken. With no plugins
	// registered, ListEnabledPluginConfigs returns empty and the DB-only
	// lookup inside ResolveWithPlugins finds nothing, triggering the fallback.
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), "identifier").
		Return(nil, nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "FX_PAIR", "", "EURGBP").
		Return("", "", "", "", nil) // not found
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "FX_PAIR", "EURGBP").
		Return("", nil) // not found

	// The key assertion: EnsureInstrument must receive "FX" and "EUR".
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "FX", "", "EUR", "", "", "",
			[]db.IdentifierInput{{Type: "FX_PAIR", Domain: "", Value: "EURGBP", Canonical: true}},
			"", nil, nil, nil).
		Return("inst-eurgbp", nil)
	// No exported_at, so the vintage is now.
	database.EXPECT().UpdateIdentityAsOf(gomock.Any(), "inst-eurgbp").Return(nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-fx").Return(nil)
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-fx", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessPriceImport_OptionFallbackResolvesUnderlying verifies that when
// identifier plugins fail for an option OCC symbol, the fallback parses the
// underlying ticker from the OCC and resolves it via DB lookup.
func TestProcessPriceImport_OptionFallbackResolvesUnderlying(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	payload := pricePayload(t, nil, &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OCC, Value: "NVDA240315P00510000"},
		AssetClass: typev1.AssetClass_OPTION,
		Currency:   "USD",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-03-01", Close: "12.50"}},
	})

	j := &JobRequest{JobID: "job-price-opt", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-opt").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-opt").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-opt", int32(1)).Return(nil)

	// No plugins registered: plugin path triggers fallback.
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), "identifier").
		Return(nil, nil)
	// OCC DB lookup finds nothing.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", "NVDA240315P00510000").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "OCC", "NVDA240315P00510000").
		Return("", nil)

	// Fallback parses underlying "NVDA" from OCC and resolves via DB.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "NVDA").
		Return("inst-nvda", "STOCK", "XNAS", "USD", nil)

	// EnsureInstrument must receive the underlying ID and option fields.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", "", "USD", "", "", "",
			[]db.IdentifierInput{{Type: "OCC", Domain: "", Value: "NVDA240315P00510000", Canonical: true}},
			"inst-nvda", nil, nil, gomock.Not(gomock.Nil())).
		Return("inst-opt", nil)
	// No exported_at on the request, so the supplied OCC is taken at face value
	// as current and the vintage is now.
	database.EXPECT().UpdateIdentityAsOf(gomock.Any(), "inst-opt").Return(nil)
	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-opt").Return(nil)
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)
	database.EXPECT().
		SetJobStatus(gomock.Any(), "job-price-opt", apiv1.JobStatus_SUCCESS).
		Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessPriceImport_OptionFallbackStampsExportedAt is the regression test
// for the price-import half of issue 0055. The fallback stores the supplied OCC
// verbatim -- ResolveWithPlugins split-adjusts its own hint list, not the value
// this path closes over -- so the identity reflects the market as of the
// request's exported_at, and that is what must be stamped.
//
// Leaving identity_as_of NULL would tell the retroactive option-split pass that
// the identity predates every split, and it would re-apply splits already baked
// into the stored OCC, dividing the strike a second time.
func TestProcessPriceImport_OptionFallbackStampsExportedAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	exportedAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	payload := pricePayload(t, timestamppb.New(exportedAt), &archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OCC, Value: "NVDA240315P00510000"},
		AssetClass: typev1.AssetClass_OPTION,
		Currency:   "USD",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-03-01", Close: "12.50"}},
	})

	j := &JobRequest{JobID: "job-price-vintage", JobType: "price"}

	database.EXPECT().LoadJobPayload(gomock.Any(), "job-price-vintage").Return(payload, nil)
	database.EXPECT().ClearJobPayload(gomock.Any(), "job-price-vintage").Return(nil)
	database.EXPECT().SetJobTotalCount(gomock.Any(), "job-price-vintage", int32(1)).Return(nil)
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), "identifier").Return(nil, nil)
	database.EXPECT().SplitsByUnderlyingTicker(gomock.Any(), "NVDA").Return(nil, nil).AnyTimes()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", "NVDA240315P00510000").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "OCC", "NVDA240315P00510000").
		Return("", nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "NVDA").
		Return("inst-nvda", "STOCK", "XNAS", "USD", nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", "", "USD", "", "", "",
			[]db.IdentifierInput{{Type: "OCC", Domain: "", Value: "NVDA240315P00510000", Canonical: true}},
			"inst-nvda", nil, nil, gomock.Not(gomock.Nil())).
		Return("inst-opt", nil)

	// The assertion: the file's declared vintage, not now() and not NULL.
	database.EXPECT().SetIdentityAsOf(gomock.Any(), "inst-opt", exportedAt).Return(nil)

	database.EXPECT().IncrJobProcessedCount(gomock.Any(), "job-price-vintage").Return(nil)
	database.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil)
	database.EXPECT().SetJobStatus(gomock.Any(), "job-price-vintage", apiv1.JobStatus_SUCCESS).Return(nil)

	if !processPriceImport(ctx, database, registry, j) {
		t.Error("expected persisted=true after a successful upsert")
	}
}
