package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
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

// pricePart wraps groups as the price part of an archive. asOf is knowledge
// time, and nil exercises the path for a document that declares none.
func pricePart(groups ...*archivev1.PriceGroup) *archivev1.PricePart {
	return &archivev1.PricePart{Groups: groups}
}

// runPricePart applies a price part with a detached reporter and returns what
// it persisted along with the problems it recorded.
func runPricePart(t *testing.T, database db.DB, registry *identifier.Registry,
	part *archivev1.PricePart, asOf *timestamppb.Timestamp) (bool, []*apiv1.ValidationError, error) {
	t.Helper()
	var at *time.Time
	if asOf != nil {
		v := asOf.AsTime()
		at = &v
	}
	rep := archiveimport.NewDetachedReporter()
	persisted, err := importPricePart(context.Background(), database, registry, part, at, newResolveCache(), nil, rep)
	return persisted, rep.Errors(), err
}

func TestProcessPriceImport_RejectsUnknownIdentifierType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// A valid enum value the resolver has no plugin vocabulary for.
	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OPENFIGI_GLOBAL, Value: "BBG000B9XRY4"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	persisted, capturedErrs, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	// Valid type passes validation, so resolveOrIdentifyInstrument is called.
	// With no asset_class and no plugins, it does DB-only lookup.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "NVDA", Domain: "XNAS"},
		Rows: []*archivev1.PriceRow{
			{PriceDate: "2024-01-15", Close: "48.0"},
			{PriceDate: "2024-01-16", ShareCountBasis: proto.String("2024-06-10"), Close: "4.8"},
		},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "NVDA").
		Return("inst-nvda", "", "XNAS", "", nil)

	var captured []db.EODPrice
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, prices []db.EODPrice) error {
			captured = prices
			return nil
		})

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "DELISTED", Domain: "XNAS"},
		Coverage:   []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-04-01"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "DELISTED").
		Return("inst-delisted", "", "XNAS", "", nil)
	database.EXPECT().
		UpsertPricesForRange(gomock.Any(), "inst-delisted", "import", gomock.Len(0),
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), gomock.Any()).
		Return(nil)

	// No bars were stored, so there is nothing for the price fetcher to react to.
	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Coverage:   []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-04-01"}},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	// Expect UpsertPricesForRange (not UpsertPrices) because coverage was provided.
	database.EXPECT().
		UpsertPricesForRange(gomock.Any(), "inst-aapl", "import", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
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

	// Coverage now sits inside the group it applies to, so a group that
	// declares none has bars covering only their own dates.
	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "", nil)
	// No coverage match for AAPL, so expect plain UpsertPrices.
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	// DB-only lookup succeeds, but the instrument has a different exchange.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNYS", "", nil) // exchange mismatch: XNYS != XNAS

	persisted, capturedErrs, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_MIC_TICKER, Value: "AAPL", Domain: "XNAS"},
		Currency:   "GBP",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-01-15", Close: "185.90"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNAS", "AAPL").
		Return("inst-aapl", "", "XNAS", "USD", nil) // currency mismatch: USD != GBP

	persisted, capturedErrs, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if persisted {
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

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_FX_PAIR, Value: "EURGBP"},
		AssetClass: typev1.AssetClass_FX,
		Currency:   "EUR",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2021-12-31", Close: "0.84"}},
	})

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
			namesDated{want: []db.IdentifierInput{{Type: "FX_PAIR", Domain: "", Value: "EURGBP", Canonical: true}}},
			"", nil, nil, nil).
		Return("inst-eurgbp", nil)
	// No exported_at, so the vintage is now.
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessPriceImport_OptionFallbackResolvesUnderlying verifies that when
// identifier plugins fail for an option OCC symbol, the fallback parses the
// underlying ticker from the OCC and resolves it via DB lookup.
// namesDated matches the identifier list EnsureInstrument is handed: the names
// themselves, plus the date they became correct. A nil from means "today",
// which is what a vintage of now reduces to and what a literal in the expectation
// could not name.
type namesDated struct {
	want []db.IdentifierInput
	from *time.Time
}

func (m namesDated) Matches(x any) bool {
	got, ok := x.([]db.IdentifierInput)
	if !ok || len(got) != len(m.want) {
		return false
	}
	from := m.from
	if from == nil {
		now := time.Now()
		from = db.VintageDate(&now)
	}
	for i, w := range m.want {
		w.ValidFrom = from
		if got[i].Type != w.Type || got[i].Domain != w.Domain || got[i].Value != w.Value ||
			got[i].Canonical != w.Canonical || got[i].ValidBefore != nil ||
			got[i].ValidFrom == nil || !got[i].ValidFrom.Equal(*w.ValidFrom) {
			return false
		}
	}
	return true
}

func (m namesDated) String() string {
	when := "today"
	if m.from != nil {
		when = m.from.Format("2006-01-02")
	}
	return fmt.Sprintf("%v valid from %s", m.want, when)
}

func TestProcessPriceImport_OptionFallbackResolvesUnderlying(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OCC, Value: "NVDA240315P00510000"},
		AssetClass: typev1.AssetClass_OPTION,
		Currency:   "USD",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-03-01", Close: "12.50"}},
	})

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
			namesDated{want: []db.IdentifierInput{{Type: "OCC", Domain: "", Value: "NVDA240315P00510000", Canonical: true}}},
			"inst-nvda", nil, nil, gomock.Not(gomock.Nil())).
		Return("inst-opt", nil)
	// No exported_at on the request, so the supplied OCC is taken at face value
	// as current and the vintage is now.
	database.EXPECT().
		UpsertPrices(gomock.Any(), gomock.Any()).
		Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, nil)
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a successful upsert")
	}
}

// TestProcessPriceImport_OptionFallbackDatesFromExportedAt is the regression test
// for the price-import half of issue 0055. The fallback stores the supplied OCC
// verbatim -- ResolveWithPlugins split-adjusts its own hint list, not the value
// this path closes over -- so the name became correct as of the request's
// exported_at, and that is the date it must carry. The namesDated matcher above
// is where the assertion lives.
//
// An undated name would tell the retroactive option-split pass that it predates
// every split, and the pass would restate a symbol already carrying them,
// dividing the strike a second time.
func TestProcessPriceImport_OptionFallbackDatesFromExportedAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	exportedAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	part := pricePart(&archivev1.PriceGroup{
		Instrument: &archivev1.InstrumentRef{Type: typev1.IdentifierType_OCC, Value: "NVDA240315P00510000"},
		AssetClass: typev1.AssetClass_OPTION,
		Currency:   "USD",
		Rows:       []*archivev1.PriceRow{{PriceDate: "2024-03-01", Close: "12.50"}},
	})

	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), "identifier").Return(nil, nil)
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
			namesDated{
				want: []db.IdentifierInput{{Type: "OCC", Domain: "", Value: "NVDA240315P00510000", Canonical: true}},
				from: db.VintageDate(&exportedAt),
			},
			"inst-nvda", nil, nil, gomock.Not(gomock.Nil())).
		Return("inst-opt", nil)

	database.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil)

	persisted, _, err := runPricePart(t, database, registry, part, timestamppb.New(exportedAt))
	if err != nil {
		t.Fatalf("importPricePart: %v", err)
	}
	if !persisted {
		t.Error("expected persisted=true after a successful upsert")
	}
}
