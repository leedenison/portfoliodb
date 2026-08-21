package identification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

// fakePlugin is a test double that returns fixed results.
type fakePlugin struct {
	inst     *identifier.Instrument
	ids      []identifier.Identifier
	filtered []identifier.Identifier
	err      error
}

func (p *fakePlugin) Identify(_ context.Context, _ []byte, _, _, _ string, _ identifier.Identity) (identifier.Result, error) {
	return identifier.Result{Instrument: p.inst, Identifiers: p.ids, Filtered: p.filtered}, p.err
}
func (p *fakePlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *fakePlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *fakePlugin) DefaultConfig() []byte                      { return nil }
func (p *fakePlugin) DisplayName() string                        { return "Fake" }

func TestResolveByHintsDBOnly_ExactMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "OPENFIGI_TICKER", "US", "AAPL").
		Return("inst-1", "", "", "", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "inst-1" {
		t.Errorf("got %v, want [inst-1]", ids)
	}
}

func TestResolveByHintsDBOnly_FallbackByTypeAndValue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	// Exact match fails (domain is empty, stored domain is "US")
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	// Fallback by (type, value) finds it
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("inst-1", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "MIC_TICKER", Domain: "", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "inst-1" {
		t.Errorf("got %v, want [inst-1]", ids)
	}
}

func TestResolveByHintsDBOnly_SkipsEmptyTypeAndValue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	// No DB calls expected for empty hints

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "", Value: "AAPL"},
		{Type: "MIC_TICKER", Value: ""},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %v, want empty", ids)
	}
}

func TestResolveByHintsDBOnly_Deduplicates(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	// Two hints resolve to the same instrument
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "OPENFIGI_TICKER", "US", "AAPL").
		Return("inst-1", "", "", "", nil)
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").
		Return("inst-1", "", "", "", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("got %d ids, want 1 (deduplicated)", len(ids))
	}
}

func TestResolveByHintsDBOnly_NormalizesOCCToCompact(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	// Padded OCC "NVDA  240315P00420000" should be normalized to compact "NVDA240315P00420000" for lookup.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", "NVDA240315P00420000").
		Return("inst-1", "", "", "", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "OCC", Value: "NVDA  240315P00420000"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "inst-1" {
		t.Errorf("got %v, want [inst-1]", ids)
	}
}

func TestFilterIdentifierHints_ValidAndInvalid(t *testing.T) {
	hints := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
		{Type: "BOGUS_TYPE", Value: "XYZ"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "", Value: "empty"},
	}
	out := FilterIdentifierHints(context.Background(), hints, nil)
	if len(out) != 2 {
		t.Fatalf("got %d hints, want 2", len(out))
	}
	if out[0].Type != "MIC_TICKER" || out[1].Type != "ISIN" {
		t.Errorf("got types %q, %q, want MIC_TICKER, ISIN", out[0].Type, out[1].Type)
	}
}

func TestFilterIdentifierHints_Nil(t *testing.T) {
	out := FilterIdentifierHints(context.Background(), nil, nil)
	if out != nil {
		t.Errorf("got %v, want nil", out)
	}
}

func TestResolveWithPlugins_DBHit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("existing-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "existing-id" {
		t.Errorf("InstrumentID = %q, want existing-id", result.InstrumentID)
	}
	if !result.Identified {
		t.Error("expected Identified = true")
	}
}

func TestResolveWithPlugins_PluginSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "new-id" {
		t.Errorf("InstrumentID = %q, want new-id", result.InstrumentID)
	}
	if !result.Identified {
		t.Error("expected Identified = true")
	}
}

// TestResolveWithPlugins_DatesNamesOnPluginSuccess pins half of the write
// discipline behind issue 0055: a plugin identification derives the names from
// current market data, so each one it writes becomes correct as of today. An
// undated name would tell the retroactive option-split pass that it predates
// every split.
func TestResolveWithPlugins_DatesNamesOnPluginSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	// The assertion: the name the plugin gave back is written dated today.
	now := time.Now()
	database.EXPECT().EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			if len(idns) != 1 {
				t.Fatalf("identifiers = %v, want just the ISIN", idns)
			}
			if idns[0].ValidFrom == nil || !idns[0].ValidFrom.Equal(*db.VintageDate(&now)) {
				t.Errorf("ISIN valid_from = %v, want today", idns[0].ValidFrom)
			}
			if idns[0].ValidBefore != nil {
				t.Errorf("ISIN valid_before = %v, want the name left in force", idns[0].ValidBefore)
			}
			return "new-id", nil
		}).Times(1)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// TestResolveWithPlugins_DatesNamesFromTheHintVintage pins the other half of
// issue 0126: a plugin answers about the instrument it was named, so a name it
// gives back is only as current as the hint that named it. The caller states
// that vintage -- the exporting file's -- and it is written verbatim, on every
// name and whether or not an OCC is among the hints. Dating a name from the run
// instead is what used to let the retroactive option-split pass conclude a
// pre-split symbol already carried the split.
func TestResolveWithPlugins_DatesNamesFromTheHintVintage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "OPTION", Currency: "USD"},
		ids: []identifier.Identifier{
			{Type: "OCC", Value: "AAPL250117C00760000"},
			{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
		},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", "AAPL250117C00760000").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "OCC", "AAPL250117C00760000").Return("", nil)
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	validAt := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	database.EXPECT().EnsureInstrument(gomock.Any(), "OPTION", "", "USD", "", "", "", gomock.Any(), gomock.Any(), "", nil, nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			if len(idns) != 2 {
				t.Fatalf("identifiers = %v, want the OCC and the ticker", idns)
			}
			for _, idn := range idns {
				if idn.ValidFrom == nil || !idn.ValidFrom.Equal(validAt) {
					t.Errorf("%s valid_from = %v, want the hint vintage %v", idn.Ref.Type, idn.ValidFrom, validAt)
				}
			}
			return "new-id", nil
		}).Times(1)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "OCC", Value: "AAPL250117C00760000"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}},
		false, nil, Attempt{}, nil, 0, &validAt); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// TestResolveWithPlugins_NoDatedNameOnFallback pins the other half: when no
// plugin identifies the instrument, the caller's fallback creates a
// broker-description-only row that reflects no market state. This path must
// write no name of its own, dated or otherwise -- dating one is what used to
// disarm the retroactive option-split guard.
func TestResolveWithPlugins_NoDatedNameOnFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	// No EnsureInstrument expectation: the strict controller fails the test if
	// this path writes a name rather than leaving it to the fallback.

	fallback := func(_ context.Context, _ db.DB) (string, error) { return "fallback-id", nil }
	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "src", "desc", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		true, fallback, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "fallback-id" {
		t.Errorf("InstrumentID = %q, want fallback-id", result.InstrumentID)
	}
}

func TestResolveWithPlugins_AllPluginsFail_Fallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "XYZ").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "XYZ").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "XYZ").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	fallbackCalled := false
	fallback := func(_ context.Context, db db.DB) (string, error) {
		fallbackCalled = true
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "XYZ"}}, Hints: identifier.Hints{}},
		false, fallback, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !fallbackCalled {
		t.Error("expected fallback to be called")
	}
	if result.InstrumentID != "fallback-id" {
		t.Errorf("InstrumentID = %q, want fallback-id", result.InstrumentID)
	}
	if result.Identified {
		t.Error("expected Identified = false")
	}
}

func TestResolveWithPlugins_Timeout_SetsHadTimeout(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "SLOW").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "SLOW").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "SLOW").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "SLOW"}}, Hints: identifier.Hints{}},
		false, fallback, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !result.HadTimeout {
		t.Error("expected HadTimeout = true")
	}
	if result.Identified {
		t.Error("expected Identified = false")
	}
}

func TestResolveWithPlugins_NilFallback_ReturnsEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "XYZ").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "XYZ").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "XYZ").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "XYZ"}}, Hints: identifier.Hints{}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "" {
		t.Errorf("InstrumentID = %q, want empty", result.InstrumentID)
	}
}

func TestResolveWithPlugins_StoreSourceDescription(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", desc).
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", desc).
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), desc).Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			hasSource := false
			for _, idn := range idns {
				if idn.Ref.Type == "BROKER_DESCRIPTION" && idn.Ref.Domain == source && idn.Ref.Value == desc && !idn.Canonical {
					hasSource = true
				}
			}
			if !hasSource {
				t.Errorf("expected BROKER_DESCRIPTION identifier for (source=%q, desc=%q)", source, desc)
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"IBKR", source, desc, identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: desc}}, Hints: identifier.Hints{}},
		true, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

func TestResolveWithPlugins_PluginError_SetsHadError(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BAD").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BAD").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "BAD").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "BAD"}}, Hints: identifier.Hints{}},
		false, fallback, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !result.HadError {
		t.Error("expected HadError = true")
	}
}

func TestHintsSummary(t *testing.T) {
	hints := []identifier.Identifier{
		{Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
	}
	got := HintsSummary(hints)
	want := "OPENFIGI_TICKER(US):AAPL, ISIN:US0378331005"
	if got != want {
		t.Errorf("HintsSummary = %q, want %q", got, want)
	}
}

func TestHintsSummary_Empty(t *testing.T) {
	got := HintsSummary(nil)
	if got != "" {
		t.Errorf("HintsSummary(nil) = %q, want empty", got)
	}
}

func TestCallPluginWithRetry_SuccessNoRetry(t *testing.T) {
	p := &fakePlugin{
		inst: &identifier.Instrument{Name: "OK"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}},
	}
	res, _, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Identity{Stated: nil, Hints: identifier.Hints{}}, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instrument.Name != "OK" {
		t.Errorf("Instrument.Name = %q, want OK", res.Instrument.Name)
	}
	if len(res.Identifiers) != 1 {
		t.Errorf("len(Identifiers) = %d, want 1", len(res.Identifiers))
	}
}

func TestCallPluginWithRetry_ErrNotIdentified_NoRetry(t *testing.T) {
	p := &fakePlugin{err: identifier.ErrNotIdentified}
	_, _, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Identity{Stated: nil, Hints: identifier.Hints{}}, time.Second, time.Millisecond)
	if !errors.Is(err, identifier.ErrNotIdentified) {
		t.Errorf("err = %v, want ErrNotIdentified", err)
	}
}

// retryPlugin fails once with a transient error, then succeeds on retry.
type retryPlugin struct {
	callCount int
	inst      *identifier.Instrument
	ids       []identifier.Identifier
}

func (p *retryPlugin) Identify(_ context.Context, _ []byte, _, _, _ string, _ identifier.Identity) (identifier.Result, error) {
	p.callCount++
	if p.callCount == 1 {
		return identifier.Result{Telemetry: identifier.Telemetry{Outcome: identifier.OutcomeError}}, errors.New("temporary failure")
	}
	return identifier.Result{
		Instrument:  p.inst,
		Identifiers: p.ids,
		Telemetry:   identifier.Telemetry{Outcome: identifier.OutcomeIdentified},
	}, nil
}

func (p *retryPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *retryPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *retryPlugin) DefaultConfig() []byte                      { return nil }
func (p *retryPlugin) DisplayName() string                        { return "Retry" }

func TestCallPluginWithRetry_RetrySucceeds(t *testing.T) {
	p := &retryPlugin{
		inst: &identifier.Instrument{Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}},
	}
	res, _, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Identity{Stated: nil, Hints: identifier.Hints{}}, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instrument.Name != "Retried" {
		t.Errorf("Instrument.Name = %q, want Retried", res.Instrument.Name)
	}
	if p.callCount != 2 {
		t.Errorf("callCount = %d, want 2", p.callCount)
	}
	// The retry that succeeded is the outcome the resolver records, not the
	// failure that preceded it.
	if res.Telemetry.Outcome != identifier.OutcomeIdentified {
		t.Errorf("Telemetry.Outcome = %q, want %q", res.Telemetry.Outcome, identifier.OutcomeIdentified)
	}
}

func TestCallPluginWithRetry_ParentCancelStopsRetry(t *testing.T) {
	// Verify that cancelling the parent context propagates to the retry attempt
	// (i.e. we no longer use context.Background() for retry).
	ctx, cancel := context.WithCancel(context.Background())
	p := &cancelOnRetryPlugin{cancel: cancel, inst: &identifier.Instrument{Name: "Never"}}
	res, _, err := callPluginWithRetry(ctx, p, nil, "", "", "X", identifier.Identity{Stated: nil, Hints: identifier.Hints{}}, time.Second, time.Millisecond)
	if err == nil {
		t.Fatalf("expected error from cancelled context, got inst=%v", res.Instrument)
	}
}

// cancelOnRetryPlugin fails the first call with a transient error (triggering retry),
// then cancels the parent context so the retry's context is also cancelled.
type cancelOnRetryPlugin struct {
	cancel    context.CancelFunc
	callCount int
	inst      *identifier.Instrument
}

func (p *cancelOnRetryPlugin) Identify(ctx context.Context, _ []byte, _, _, _ string, _ identifier.Identity) (identifier.Result, error) {
	p.callCount++
	if p.callCount == 1 {
		p.cancel()
		return identifier.Result{}, errors.New("transient")
	}
	if ctx.Err() != nil {
		return identifier.Result{}, ctx.Err()
	}
	return identifier.Result{Instrument: p.inst}, nil
}

func (p *cancelOnRetryPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *cancelOnRetryPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *cancelOnRetryPlugin) DefaultConfig() []byte                      { return nil }
func (p *cancelOnRetryPlugin) DisplayName() string                        { return "CancelOnRetry" }

// --- consistentWith tests ---

func TestConsistentWith_AllMatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected consistent")
	}
}

func TestConsistentWith_CurrencyMismatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Currency: "USD"},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Currency: "EUR"},
	}
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected inconsistent on currency mismatch")
	}
}

func TestConsistentWith_ExchangeMismatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNYS"}, Currency: "USD"},
	}
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected inconsistent on exchange mismatch")
	}
}

func TestConsistentWith_EmptyFieldsSkipped(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: ""}, Currency: ""},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected consistent when other has empty exchange/currency")
	}
}

func TestConsistentWith_IdentifierValueMismatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB1234567890"}},
	}
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected inconsistent on ISIN value mismatch")
	}
}

func TestConsistentWith_IdentifierValueMatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected consistent when ISIN values match")
	}
}

func TestResolveWithPlugins_InconsistentPluginExcluded(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): XNAS/USD with ISIN
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	// Plugin B (lower precedence): XNYS/EUR with FIGI -- inconsistent
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Currency: "EUR", Name: "Pomme"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG999999999"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			for _, idn := range idns {
				if idn.Ref.Type == "OPENFIGI_SHARE_CLASS" {
					t.Errorf("OPENFIGI_GLOBAL from inconsistent plugin should not be merged, got %q", idn.Ref.Value)
				}
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// --- CompareHints tests ---

func TestCompareHints_NoDiffs(t *testing.T) {
	hints := identifier.Hints{Currency: "USD", SecurityTypeHint: "STOCK"}
	inst := &identifier.Instrument{Currency: "USD", AssetClass: "STOCK"}
	idnHints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	resolvedIDs := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}

	diffs := CompareHints(context.Background(), hints, idnHints, inst, resolvedIDs, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %v", diffs)
	}
}

func TestCompareHints_CurrencyMismatch(t *testing.T) {
	hints := identifier.Hints{Currency: "USD"}
	inst := &identifier.Instrument{Currency: "EUR"}

	diffs := CompareHints(context.Background(), hints, nil, inst, nil, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Field != "Currency" || diffs[0].HintValue != "USD" || diffs[0].ResolvedValue != "EUR" {
		t.Errorf("unexpected diff: %+v", diffs[0])
	}
}

func TestCompareHints_CurrencyCaseInsensitive(t *testing.T) {
	hints := identifier.Hints{Currency: "usd"}
	inst := &identifier.Instrument{Currency: "USD"}

	diffs := CompareHints(context.Background(), hints, nil, inst, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs for case-insensitive match, got %v", diffs)
	}
}

func TestCompareHints_EmptyCurrencySkipped(t *testing.T) {
	// Empty hint currency.
	diffs := CompareHints(context.Background(), identifier.Hints{}, nil, &identifier.Instrument{Currency: "USD"}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when hint currency empty, got %v", diffs)
	}
	// Empty resolved currency.
	diffs = CompareHints(context.Background(), identifier.Hints{Currency: "USD"}, nil, &identifier.Instrument{}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when resolved currency empty, got %v", diffs)
	}
}

func TestCompareHints_SecurityTypeMismatch(t *testing.T) {
	hints := identifier.Hints{SecurityTypeHint: "STOCK"}
	inst := &identifier.Instrument{AssetClass: "ETF"}

	diffs := CompareHints(context.Background(), hints, nil, inst, nil, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Field != "SecurityType" || diffs[0].HintValue != "STOCK" || diffs[0].ResolvedValue != "ETF" {
		t.Errorf("unexpected diff: %+v", diffs[0])
	}
}

func TestCompareHints_SecurityTypeUnknownSkipped(t *testing.T) {
	// UNKNOWN hint should not produce a diff.
	diffs := CompareHints(context.Background(), identifier.Hints{SecurityTypeHint: "UNKNOWN"}, nil, &identifier.Instrument{AssetClass: "STOCK"}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when hint is UNKNOWN, got %v", diffs)
	}
	// UNKNOWN resolved should not produce a diff.
	diffs = CompareHints(context.Background(), identifier.Hints{SecurityTypeHint: "STOCK"}, nil, &identifier.Instrument{AssetClass: "UNKNOWN"}, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when resolved is UNKNOWN, got %v", diffs)
	}
}

func TestCompareHints_ExchangeViaMICTickerDomain(t *testing.T) {
	hints := identifier.Hints{}
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNYS"}}

	diffs := CompareHints(context.Background(), hints, idnHints, inst, nil, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Field != "Exchange" || diffs[0].HintValue != "XNAS" || diffs[0].ResolvedValue != "XNYS" {
		t.Errorf("unexpected diff: %+v", diffs[0])
	}
}

func TestCompareHints_ExchangeViaMICTickerMatch(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %v", diffs)
	}
}

func TestCompareHints_ExchangeEmptyDomainSkipped(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "", Value: "AAPL"}}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when MIC_TICKER domain empty, got %v", diffs)
	}
}

func TestCompareHints_IdentifierValueMismatch(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	resolvedIDs := []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, &identifier.Instrument{}, resolvedIDs, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Field != "ISIN" || diffs[0].HintValue != "US0378331005" || diffs[0].ResolvedValue != "GB0002634946" {
		t.Errorf("unexpected diff: %+v", diffs[0])
	}
}

func TestCompareHints_IdentifierTypeNotInResolved(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "CUSIP", Value: "037833100"}}
	resolvedIDs := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, &identifier.Instrument{}, resolvedIDs, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs when hint type not in resolved, got %v", diffs)
	}
}

func TestCompareHints_MultipleDiffs(t *testing.T) {
	hints := identifier.Hints{Currency: "USD", SecurityTypeHint: "STOCK"}
	idnHints := []identifier.Identifier{
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
	}
	inst := &identifier.Instrument{Currency: "EUR", AssetClass: "ETF", Venue: identifier.Venue{MIC: "XNYS"}}
	resolvedIDs := []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}}

	diffs := CompareHints(context.Background(), hints, idnHints, inst, resolvedIDs, nil)
	if len(diffs) != 4 {
		t.Fatalf("expected 4 diffs, got %d: %v", len(diffs), diffs)
	}
	fields := make(map[string]bool)
	for _, d := range diffs {
		fields[d.Field] = true
	}
	for _, f := range []string{"Currency", "SecurityType", "Exchange", "ISIN"} {
		if !fields[f] {
			t.Errorf("expected diff for %s", f)
		}
	}
}

func TestCompareHints_NilInstrument(t *testing.T) {
	diffs := CompareHints(context.Background(), identifier.Hints{Currency: "USD"}, nil, nil, nil, nil)
	if diffs != nil {
		t.Errorf("expected nil diffs for nil instrument, got %v", diffs)
	}
}

func TestResolveWithPlugins_ConsistentPluginsMerged(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): XNAS/USD with ISIN
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	// Plugin B (lower precedence): XNAS/USD with FIGI -- consistent
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			hasFIGI := false
			for _, idn := range idns {
				if idn.Ref.Type == "OPENFIGI_SHARE_CLASS" && idn.Ref.Value == "BBG000B9XRY4" {
					hasFIGI = true
				}
			}
			if !hasFIGI {
				t.Error("expected OPENFIGI_GLOBAL from consistent plugin to be merged")
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// --- MIC normalization tests ---

func testMICNormalizer() MICNormalizer {
	mapping := map[string]string{
		"XNGS": "XNAS", "XNMS": "XNAS", "XNAS": "XNAS",
		"ARCX": "XNYS", "XNYS": "XNYS",
	}
	return func(_ context.Context, mic string) string {
		if op, ok := mapping[mic]; ok {
			return op
		}
		return mic
	}
}

func TestConsistentWith_SegmentVsOperatingMIC(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XNGS"}, Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8"}},
	}
	// Without normalizer: different exchanges are inconsistent.
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil, nil) {
		t.Error("expected inconsistent without normalizer")
	}
	// With normalizer: XNGS and XNAS map to the same operating MIC.
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected consistent with normalizer (XNGS -> XNAS)")
	}
}

func TestCompareHints_SegmentMICNormalized(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNGS", Value: "AAPL"}}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}}

	// Without normalizer: segment vs operating produces a diff.
	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, nil)
	if len(diffs) != 1 || diffs[0].Field != "Exchange" {
		t.Fatalf("expected Exchange diff without normalizer, got %v", diffs)
	}

	// With normalizer: both normalize to XNAS, no diff.
	diffs = CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, testMICNormalizer())
	if len(diffs) != 0 {
		t.Errorf("expected no diffs with normalizer, got %v", diffs)
	}
}

// --- resultMatchesHints tests ---

func TestResultMatchesHints_CurrencyConfirmed(t *testing.T) {
	r := &pluginResult{
		inst: &identifier.Instrument{Currency: "USD", Venue: identifier.Venue{MIC: "XNAS"}},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	hints := identifier.Hints{Currency: "USD"}
	if !resultMatchesHints(context.Background(), hints, nil, r, nil) {
		t.Error("expected match when currency confirmed")
	}
}

func TestResultMatchesHints_CurrencyMismatch(t *testing.T) {
	r := &pluginResult{
		inst: &identifier.Instrument{Currency: "EUR"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	hints := identifier.Hints{Currency: "USD"}
	if resultMatchesHints(context.Background(), hints, nil, r, nil) {
		t.Error("expected no match when currency differs")
	}
}

func TestResultMatchesHints_SparseResultNoConfirmation(t *testing.T) {
	// Plugin returns empty currency and exchange -- no field is confirmed.
	r := &pluginResult{
		inst: &identifier.Instrument{Currency: "", Venue: identifier.Venue{MIC: ""}, AssetClass: "STOCK"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	hints := identifier.Hints{Currency: "GBX"}
	if resultMatchesHints(context.Background(), hints, nil, r, nil) {
		t.Error("expected no match when result is too sparse to confirm any hint")
	}
}

func TestResultMatchesHints_ExchangeConfirmed(t *testing.T) {
	r := &pluginResult{
		inst: &identifier.Instrument{Venue: identifier.Venue{MIC: "XLON"}, Currency: ""},
		ids:  []identifier.Identifier{},
	}
	hints := identifier.Hints{}
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "BA"}}
	if !resultMatchesHints(context.Background(), hints, idnHints, r, nil) {
		t.Error("expected match when exchange confirmed via MIC_TICKER")
	}
}

func TestResultMatchesHints_IdentifierValueConfirmed(t *testing.T) {
	r := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	hints := identifier.Hints{}
	idnHints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	if !resultMatchesHints(context.Background(), hints, idnHints, r, nil) {
		t.Error("expected match when identifier value confirmed")
	}
}

func TestResultMatchesHints_NilInstrument(t *testing.T) {
	r := &pluginResult{inst: nil}
	if resultMatchesHints(context.Background(), identifier.Hints{Currency: "USD"}, nil, r, nil) {
		t.Error("expected no match for nil instrument")
	}
}

// --- Winner selection with hint preference tests ---

// resolveWithPluginsTestSetup creates common mock expectations for winner selection tests.
func resolveWithPluginsTestSetup(t *testing.T) (*gomock.Controller, *mock.MockDB) {
	t.Helper()
	ctrl := gomock.NewController(t)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, mic string) (string, error) { return mic, nil },
	).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return ctrl, database
}

func TestResolveWithPlugins_HintMatchPrefersLowerPrecedence(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl, database := resolveWithPluginsTestSetup(t)
	defer ctrl.Finish()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): wrong exchange/currency
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "BAC"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0605051046"}},
	})
	// Plugin B (lower precedence): matches hints (XLON/GBX)
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBX", Name: "BAE Systems"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XLON", "BA").
		Return("", "", "", "", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	// Expect the lower-precedence plugin's data (XLON/GBX/BAE Systems).
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XLON", "GBX", "BAE Systems", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("id-bae", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "BA"}}, Hints: identifier.Hints{Currency: "GBX"}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "id-bae" {
		t.Errorf("expected id-bae, got %s", result.InstrumentID)
	}
	if len(result.HintDiffs) != 0 {
		t.Errorf("expected no hint diffs, got %v", result.HintDiffs)
	}
}

func TestResolveWithPlugins_NoHintMatch_FallsBackToPrecedence(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl, database := resolveWithPluginsTestSetup(t)
	defer ctrl.Finish()
	registry := identifier.NewRegistry()

	// Both plugins return wrong currency -- neither matches hints.
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Currency: "EUR", Name: "Apple EU"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "EU0000000001"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	// Highest precedence (pluginA) should win.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("id-apple", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{Currency: "GBX"}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "id-apple" {
		t.Errorf("expected id-apple, got %s", result.InstrumentID)
	}
}

func TestResolveWithPlugins_NoHints_PurePrecedence(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl, database := resolveWithPluginsTestSetup(t)
	defer ctrl.Finish()
	registry := identifier.NewRegistry()

	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBX", Name: "BAE"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	// No hints: highest precedence wins.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("id-apple", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "id-apple" {
		t.Errorf("expected id-apple, got %s", result.InstrumentID)
	}
}

func TestResolveWithPlugins_AllMatch_HighestPrecedenceWins(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl, database := resolveWithPluginsTestSetup(t)
	defer ctrl.Finish()
	registry := identifier.NewRegistry()

	// Both plugins match hints (XLON/GBX).
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBX", Name: "BAE Systems A"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}},
	})
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBX", Name: "BAE Systems B"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0002634946"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XLON", "BA").
		Return("", "", "", "", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	// Both match, highest precedence (pluginA) wins.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XLON", "GBX", "BAE Systems A", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("id-bae-a", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "BA"}}, Hints: identifier.Hints{Currency: "GBX"}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "id-bae-a" {
		t.Errorf("expected id-bae-a, got %s", result.InstrumentID)
	}
}

func TestResolveWithPlugins_SparseResultDoesNotVacuouslyMatch(t *testing.T) {
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl, database := resolveWithPluginsTestSetup(t)
	defer ctrl.Finish()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): rich data, currency mismatch with hints.
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	// Plugin B (lower precedence): sparse -- empty currency/exchange.
	// Zero diffs vacuously but should NOT win because no field is confirmed.
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Currency: "", Venue: identifier.Venue{MIC: ""}, Name: "Unknown"},
		ids:  []identifier.Identifier{{Type: "SEDOL", Value: "B0YQ5W0"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XLON", "BA").
		Return("", "", "", "", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	// Sparse result should not beat rich result; pluginA wins by precedence.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("id-apple", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "BA"}}, Hints: identifier.Hints{Currency: "GBX"}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if result.InstrumentID != "id-apple" {
		t.Errorf("expected id-apple (highest precedence), got %s", result.InstrumentID)
	}
}

// An underlying hint built from an OCC root spells a multi-class ticker without
// its separator, so BRKB has to reach the BRK.B the same import already
// resolved. Without this the resolver falls through to the plugins, where a
// bare root matches that ticker on every venue in the world.
func TestResolveByHintsDBOnly_OCCRootReachesSplitTicker(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BRKB").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BRKB").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "BRKB").
		Return("inst-brk", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "BRKB"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "inst-brk" {
		t.Fatalf("got %+v, want one instrument inst-brk", ids)
	}
}

// The separator-insensitive lookup is a last resort: an exact match must not
// reach it, or a ticker that genuinely differs only by a separator would be
// answered by whichever row the looser query happened to find first.
func TestResolveByHintsDBOnly_ExactMatchSkipsSeparatorFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("inst-aapl", "STOCK", "XNAS", "USD", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "inst-aapl" {
		t.Fatalf("got %+v, want one instrument inst-aapl", ids)
	}
}

func TestOptionFieldsFromIdentifiers(t *testing.T) {
	ids := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
		{Type: "OCC", Value: "AAPL251219C00230000"},
	}
	got := optionFieldsFromIdentifiers(ids)
	if got == nil {
		t.Fatal("expected non-nil OptionFields")
	}
	if got.Strike.String() != "230" {
		t.Errorf("strike = %v, want 230", got.Strike)
	}
	if got.PutCall != "C" {
		t.Errorf("put_call = %q, want C", got.PutCall)
	}
	if got.Expiry.IsZero() {
		t.Error("expiry is zero")
	}
}

func TestOptionFieldsFromIdentifiers_NoOCC(t *testing.T) {
	ids := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "AAPL"},
	}
	got := optionFieldsFromIdentifiers(ids)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// A higher-precedence plugin that answers with a composite names no venue, so
// the exchange it supplies is empty while a lower-precedence result names the
// venue outright. Without the fill the instrument is stored with a null
// exchange_mic beside a MIC_TICKER whose domain says exactly which exchange it
// is -- the divergence 0099 is about, written by the same statement.
func TestResolveWithPlugins_WinnerBlanksFilledFromConsistentLoser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	// Highest precedence: a composite answer. No venue, no currency, no CIK.
	registry.Register("composite", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "BERKSHIRE HATHAWAY INC-CL B"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_TICKER", Domain: "US", Value: "BRK/B"}},
	})
	// Lower precedence: names the venue and the currency.
	registry.Register("venue", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Currency: "USD", Name: "Berkshire Hathaway Inc", CIK: "0001067983"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "BRK.B"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BRK.B").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BRK.B").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "BRK.B").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "composite", Precedence: 20}, {PluginID: "venue", Precedence: 10}}, nil)
	// The winner still supplies the name it gave; only the blanks are filled.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNYS", "USD", "BERKSHIRE HATHAWAY INC-CL B", "0001067983", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK.B"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !result.Identified {
		t.Error("expected Identified = true")
	}
}

// A field the winner did supply is never replaced: adr/0004 makes the
// identifier the source of truth for the instrument it matched.
func TestResolveWithPlugins_WinnerValuesNotOverwrittenByLoser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("winner", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBP", Name: "Winner Plc"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "WIN"}},
	})
	// Same exchange, so consistentWith keeps it; its name and currency differ
	// and must not displace the winner's.
	registry.Register("loser", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBP", Name: "Loser Ltd"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0000000001"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "WIN").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "WIN").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "WIN").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "winner", Precedence: 20}, {PluginID: "loser", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XLON", "GBP", "Winner Plc", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "WIN"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// An inconsistent result contributes nothing, fills included -- it is excluded
// from the merge entirely, not merely outranked.
func TestResolveWithPlugins_InconsistentLoserFillsNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("winner", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Currency: "GBP", Name: "Winner Plc"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "", Value: "WIN"}},
	})
	// Contradicts the winner's currency, so it is discarded before any fill.
	registry.Register("other", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XSTO"}, Currency: "SEK", Name: "Other AB"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "SE0000000001"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "WIN").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "WIN").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "WIN").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "winner", Precedence: 20}, {PluginID: "other", Precedence: 10}}, nil)
	// Exchange stays empty: XSTO came from a result excluded from the merge.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "GBP", "Winner Plc", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "WIN"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// The hazard a market-level answer exists to catch. A plugin answering with a
// composite names no venue, so a comparison that looks only at MICs reads it as
// no opinion and a foreign listing passes as consistent -- and then fills its
// venue onto the instrument and merges its ISIN. The answer says the security
// trades in the United States, and XLON is not there.
func TestResolveWithPlugins_ForeignVenueContradictsTheWinnersMarket(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("composite", &fakePlugin{
		inst: &identifier.Instrument{
			AssetClass: "STOCK",
			Name:       "US LISTED CO",
			Venue:      identifier.Venue{Country: "US"},
		},
		ids: []identifier.Identifier{{Type: "OPENFIGI_TICKER", Domain: "US", Value: "X"}},
	})
	// Same ticker, different company, London. No overlapping identifier type
	// with the winner and no currency hint, so nothing else would catch it.
	registry.Register("foreign", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XLON"}, Currency: "GBP", Name: "Unrelated Plc"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "GB0000000001"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "X").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "X").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "X").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "composite", Precedence: 20}, {PluginID: "foreign", Precedence: 10}}, nil)
	// Exchange stays empty and the London currency is not adopted. The ISIN is
	// checked separately below.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "US LISTED CO", "", "",
			gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, ids []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			for _, id := range ids {
				if id.Ref.Type == "ISIN" {
					t.Errorf("ISIN %q merged from a listing outside the market the winner named", id.Ref.Value)
				}
			}
			return "new-id", nil
		})

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// A venue inside the market named is exactly what the fill is for.
func TestResolveWithPlugins_VenueInsideTheMarketIsAdopted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("composite", &fakePlugin{
		inst: &identifier.Instrument{
			AssetClass: "STOCK",
			Name:       "BERKSHIRE HATHAWAY INC-CL B",
			Venue:      identifier.Venue{Country: "US"},
		},
		ids: []identifier.Identifier{{Type: "OPENFIGI_TICKER", Domain: "US", Value: "BRK/B"}},
	})
	registry.Register("venue", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Currency: "USD", Name: "Berkshire Hathaway Inc"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "BRK.B"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BRK.B").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BRK.B").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "BRK.B").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "composite", Precedence: 20}, {PluginID: "venue", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNYS", "USD", "BERKSHIRE HATHAWAY INC-CL B", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "BRK.B"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// testMICCountry answers the MIC-to-country question the way the seeded ISO
// reference data does, for the venues these tests name.
func testMICCountry(_ context.Context, mic string) (string, error) {
	switch mic {
	case "XNYS", "XNAS", "OTCM", "XCIS", "BATS", "IEXG":
		return "US", nil
	case "XLON":
		return "GB", nil
	case "XSTO":
		return "SE", nil
	case "XWBO":
		return "AT", nil
	}
	return "", nil
}

// --- a proposed identifier is not evidence (adr/0057) ---

// The database is never asked about a proposal. A hit would resolve the
// instrument before any plugin ran, so the proposal would have decided the
// answer by being looked up -- the one thing it must never do. The mock has no
// expectation for the proposed value, so a lookup of it fails the test rather
// than quietly succeeding.
func TestResolveWithPlugins_ProposalNeverSatisfiesTheDBLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Real Co"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "REAL"}},
	})

	// Only the stated hint is looked up, and it misses.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "REAL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "REAL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "REAL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Real Co", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	ident := identifier.Identity{
		Stated:   []identifier.Identifier{{Type: "MIC_TICKER", Value: "REAL"}},
		Proposed: []identifier.Identifier{{Type: "ISIN", Value: "US0000000001"}},
		Hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
	}
	res, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", ident, false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if res.InstrumentID != "new-id" {
		t.Errorf("InstrumentID = %q, want new-id", res.InstrumentID)
	}
}

// A proposal is never written. It is not among the identifiers EnsureInstrument
// is given, so it cannot be stored and cannot draw a second instrument into the
// eager merge in adr/0004.
func TestResolveWithPlugins_ProposalIsNeverPersisted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Real Co"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "REAL"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "REAL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "REAL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "REAL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Real Co", "", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, ids []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			for _, id := range ids {
				if id.Ref.Type == "ISIN" {
					t.Errorf("proposed ISIN %q reached EnsureInstrument", id.Ref.Value)
				}
			}
			return "new-id", nil
		})

	ident := identifier.Identity{
		Stated:   []identifier.Identifier{{Type: "MIC_TICKER", Value: "REAL"}},
		Proposed: []identifier.Identifier{{Type: "ISIN", Value: "US0000000001"}},
		Hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
	}
	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", ident, false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}

// resolveWinnerTiers registers three plugins in precedence order and returns the
// name of the instrument the resolver chose. plugA has the highest precedence
// and agrees with nothing.
//
// Every caller states an identifier, because the tiers only exist where one was
// stated. Where a source states nothing the proposal becomes the key that gets
// queried, and agreeing with it is agreeing with what was asked -- there is no
// second provenance left to rank by. See adr/0057.
func resolveWinnerTiers(t *testing.T, ident identifier.Identity) string {
	t.Helper()
	ident.Stated = append([]identifier.Identifier{{Type: "ISIN", Value: "US0000000001"}}, ident.Stated...)
	t.Helper()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	// Each returns a distinct ticker, so a result matches a tier only by the
	// thing that tier is about -- otherwise every plugin agrees with every
	// identifier and the tiers stop discriminating.
	//
	// Highest precedence, agrees with nothing anyone said.
	registry.Register("precedence", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Currency: "JPY", Name: "By precedence"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "Y"}},
	})
	// Agrees with the proposed venue.
	registry.Register("proposed", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Name: "By proposal"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
	})
	// Agrees with the stated currency.
	registry.Register("stated", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Currency: "GBP", Name: "By statement"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "Z"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", "", "", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "precedence", Precedence: 30},
			{PluginID: "proposed", Precedence: 20},
			{PluginID: "stated", Precedence: 10},
		}, nil)

	var chosen string
	database.EXPECT().
		EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, name, _, _ string, _ []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			chosen = name
			return "id", nil
		})

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", ident, false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	return chosen
}

// Agreeing with what a source stated outranks agreeing with what a plugin
// proposed, even though the proposal-matching plugin has higher precedence.
func TestResolveWithPlugins_StatedMatchBeatsProposedMatch(t *testing.T) {
	got := resolveWinnerTiers(t, identifier.Identity{
		Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
		Hints:    identifier.Hints{Currency: "GBP"},
	})
	if got != "By statement" {
		t.Errorf("winner = %q, want the plugin agreeing with the stated currency", got)
	}
}

// With nothing stated to separate them, agreeing with a proposal breaks the tie
// ahead of precedence alone.
func TestResolveWithPlugins_ProposedMatchBeatsPrecedence(t *testing.T) {
	got := resolveWinnerTiers(t, identifier.Identity{
		Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
	})
	if got != "By proposal" {
		t.Errorf("winner = %q, want the plugin agreeing with the proposed venue", got)
	}
}

// A proposal that nothing agrees with costs a result its place in the middle
// tier and nothing more: precedence still decides, and no result is removed
// from contention by contradicting a guess.
func TestResolveWithPlugins_ContradictedProposalFallsBackToPrecedence(t *testing.T) {
	got := resolveWinnerTiers(t, identifier.Identity{
		Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XSTO", Value: "X"}},
	})
	if got != "By precedence" {
		t.Errorf("winner = %q, want the highest-precedence plugin", got)
	}
}

// A guess must not promote a result that argues with the source. The
// proposal-matching plugin contradicts the stated currency; the highest-precedence
// one merely says nothing about it. Without the contradiction test the first
// would win, which is a proposal outranking a statement by the back door.
func TestResolveWithPlugins_ProposalDoesNotPromoteAResultContradictingTheSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	// Highest precedence. Says nothing about the currency, so it confirms
	// nothing and contradicts nothing.
	registry.Register("silent", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Says nothing"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "Q"}},
	})
	// Agrees with the proposed venue and disagrees with the stated currency.
	registry.Register("contradicts", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNYS"}, Currency: "USD", Name: "Argues with the source"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", "", "", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "silent", Precedence: 30}, {PluginID: "contradicts", Precedence: 10}}, nil)

	var chosen string
	database.EXPECT().
		EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, name, _, _ string, _ []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			chosen = name
			return "id", nil
		})

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{
			Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
			Hints:    identifier.Hints{Currency: "GBP"},
		},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if chosen != "Says nothing" {
		t.Errorf("winner = %q, want the plugin that does not contradict the stated currency", chosen)
	}
}

// --- NewProposalFilter ---

// filterAll applies the filter to each identifier in turn and collects what
// survived, which is what the callers do with their own metadata attached.
func filterAll(t *testing.T, database db.InstrumentDB, in []identifier.Identifier) []identifier.Identifier {
	t.Helper()
	f := NewProposalFilter(context.Background(), database, nil)
	var out []identifier.Identifier
	for _, id := range in {
		if kept, ok := f(id); ok {
			out = append(out, kept)
		}
	}
	return out
}

// A candidate plugin has no database access, so a venue it proposes has been
// checked against nothing. An unknown MIC costs the domain and keeps the ticker:
// a good symbol under a venue that does not exist is still worth having, and
// dropping the pair would throw away the half that was right.
func TestProposalFilter_UnknownMICLosesTheDomainAndKeepsTheTicker(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ValidateMIC(gomock.Any(), "XZZZ").Return(false, nil)

	got := filterAll(t, database,
		[]identifier.Identifier{{Type: "MIC_TICKER", Domain: "XZZZ", Value: "AAPL"}})

	if len(got) != 1 {
		t.Fatalf("got %d identifiers, want the ticker to survive", len(got))
	}
	if got[0].Domain != "" {
		t.Errorf("Domain = %q, want empty: XZZZ is not an exchange", got[0].Domain)
	}
	if got[0].Value != "AAPL" {
		t.Errorf("Value = %q, want AAPL", got[0].Value)
	}
}

// A recognised segment MIC is normalised to its operating MIC, so a proposal is
// compared and stored at the grain everything else uses (adr/0003).
func TestProposalFilter_KnownMICIsNormalisedToItsOperatingMIC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ValidateMIC(gomock.Any(), "XNGS").Return(true, nil)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), "XNGS").Return("XNAS", nil)

	got := filterAll(t, database,
		[]identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNGS", Value: "AAPL"}})

	if len(got) != 1 || got[0].Domain != "XNAS" {
		t.Fatalf("got %+v, want the domain normalised to XNAS", got)
	}
}

// The same venue proposed across a batch is looked up once: a batch proposes the
// same handful of exchanges over and over, and the reference table does not
// change underneath it. gomock enforces the count.
func TestProposalFilter_LookupsAreMemoised(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().ValidateMIC(gomock.Any(), "XLON").Return(true, nil).Times(1)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), "XLON").Return("XLON", nil).Times(1)

	got := filterAll(t, database, []identifier.Identifier{
		{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"},
		{Type: "MIC_TICKER", Domain: "XLON", Value: "BP"},
		{Type: "MIC_TICKER", Domain: "XLON", Value: "HSBA"},
	})

	if len(got) != 3 {
		t.Fatalf("got %d identifiers, want 3", len(got))
	}
}

// A type outside the controlled vocabulary is dropped, as it is for a stated
// hint: FilterProposals is that filter plus the checks needing reference data.
func TestProposalFilter_DropsTypesOutsideTheVocabulary(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	got := filterAll(t, database,
		[]identifier.Identifier{{Type: "NOT_A_REAL_TYPE", Value: "x"}})

	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// A source that stated an identifier is not subject to the round-trip gate. The
// proposal there was never queried -- it only ranked among listings the stated
// key produced -- so there is no invention to round-trip, and a sparse result is
// kept exactly as it was before 0132.
func TestResolveWithPlugins_AStatedKeyIsNotSubjectToTheRoundTrip(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", "", "", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "p", Precedence: 10}}, nil)
	// Confirms nothing: no asset class, no currency, no venue.
	registry := identifier.NewRegistry()
	registry.Register("p", &fakePlugin{
		inst: &identifier.Instrument{Name: "Says nothing"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}},
	})
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "Says nothing",
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("kept-id", nil)

	fallback := func(context.Context, db.DB) (string, error) {
		t.Fatal("the fallback ran: a stated key was subjected to the round-trip gate")
		return "", nil
	}
	got, err := ResolveWithPlugins(context.Background(), database, registry, "", "", "",
		identifier.Identity{
			Stated:   []identifier.Identifier{{Type: "ISIN", Value: "US0000000001"}},
			Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "X"}},
		}, false, fallback, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !got.Identified || got.InstrumentID != "kept-id" {
		t.Errorf("result = %+v, want the sparse result kept", got)
	}
	if got.Unconfirmed {
		t.Error("Unconfirmed set for an identity the source stated")
	}
}

// A proposal is never counted as its own confirmation. Where a source stated
// nothing the proposal is the key the plugins were queried with, so a provider
// returning it back is the answer agreeing with the question.
func TestConfirmedFields_AProposalDoesNotConfirmItself(t *testing.T) {
	inst := &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}}
	ids := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	// Passed as stated, the ticker and its venue both count.
	stated := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	if got := confirmedFields(context.Background(), identifier.Hints{}, stated, inst, ids, nil); len(got) != 2 {
		t.Errorf("confirmed = %v, want the venue and the ticker", got)
	}
	// The same values as a proposal are not passed here at all, which is the
	// point: confirmedFields is only ever given what a source stated.
	if got := confirmedFields(context.Background(), identifier.Hints{}, nil, inst, ids, nil); len(got) != 0 {
		t.Errorf("confirmed = %v, want nothing", got)
	}
}

// A guessed venue must not narrow the database lookup. A price import stores a
// ticker and no venue; a later transaction whose candidate plugin proposes the
// same ticker with a venue has to find that instrument rather than fork a second
// one beside it. The plugins still receive the venue -- only the lookup widens.
func TestResolveWithPlugins_AGuessedVenueDoesNotNarrowTheLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	// The stored instrument carries the ticker with no venue, which is what a
	// price-first import leaves behind. The lookup must reach it.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("price-first-id", nil)

	got, err := ResolveWithPlugins(context.Background(), database, identifier.NewRegistry(), "", "", "",
		identifier.Identity{
			Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
			Hints:    identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		}, false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if got.InstrumentID != "price-first-id" {
		t.Errorf("InstrumentID = %q, want the instrument the ticker already names", got.InstrumentID)
	}
}

// A stated venue is evidence and does narrow the lookup: naming XNYS and finding
// an instrument on XNAS is a real difference, not a guess to be widened away.
func TestResolveWithPlugins_AStatedVenueDoesNarrowTheLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	// Only the exact lookup is expected: no widening, so no FindInstrumentByTypeAndValue.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "XNYS", "AAPL").
		Return("stated-id", "STOCK", "XNYS", "USD", nil)

	got, err := ResolveWithPlugins(context.Background(), database, identifier.NewRegistry(), "", "", "",
		identifier.Identity{
			Stated: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"}},
		}, false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if got.InstrumentID != "stated-id" {
		t.Errorf("InstrumentID = %q, want stated-id", got.InstrumentID)
	}
}

// --- per-field candidate telemetry (0134) ---

// The verdict is about the identifier as a whole, venue included, because that
// is what was proposed: confirming the symbol while the venue disagrees is not a
// confirmation of what was said.
func TestProposalOutcomes(t *testing.T) {
	inst := func(venue, currency string) *identifier.Instrument {
		return &identifier.Instrument{Currency: currency, Venue: identifier.Venue{MIC: venue}}
	}
	ids := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}

	cases := []struct {
		name     string
		proposed identifier.Identifier
		inst     *identifier.Instrument
		resolved []identifier.Identifier
		want     string
	}{
		{"symbol and venue agree", identifier.Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
			inst("XNAS", "USD"), ids, db.TelemetryCandidateFieldConfirmed},
		{"symbol agrees, venue does not", identifier.Identifier{Type: "MIC_TICKER", Domain: "XNYS", Value: "AAPL"},
			inst("XNAS", "USD"), ids, db.TelemetryCandidateFieldContradicted},
		{"symbol differs", identifier.Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "MSFT"},
			inst("XNAS", "USD"), ids, db.TelemetryCandidateFieldContradicted},
		{"venue differs, symbol not carried", identifier.Identifier{Type: "MIC_TICKER", Domain: "XLON", Value: "AAPL"},
			inst("XNAS", "USD"), nil, db.TelemetryCandidateFieldContradicted},
		{"result says nothing", identifier.Identifier{Type: "MIC_TICKER", Value: "AAPL"},
			inst("", ""), nil, db.TelemetryCandidateFieldUntested},
		{"currency agrees", identifier.Identifier{Type: "CURRENCY", Value: "USD"},
			inst("XNAS", "USD"), nil, db.TelemetryCandidateFieldConfirmed},
		{"currency differs", identifier.Identifier{Type: "CURRENCY", Value: "GBX"},
			inst("XNAS", "USD"), nil, db.TelemetryCandidateFieldContradicted},
		{"currency unknown to the result", identifier.Identifier{Type: "CURRENCY", Value: "USD"},
			inst("XNAS", ""), nil, db.TelemetryCandidateFieldUntested},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := proposalOutcomes(context.Background(), []identifier.Identifier{c.proposed}, c.inst, c.resolved, nil)
			if len(got) != 1 || got[0].Outcome != c.want {
				t.Errorf("outcome = %v, want %q", got, c.want)
			}
		})
	}
}

// Nothing resolved means nothing checked the proposal, which is unused rather
// than untested: the two have different fixes and are kept apart.
func TestProposalOutcomes_NothingResolvedIsUnused(t *testing.T) {
	got := proposalOutcomes(context.Background(),
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, nil, nil, nil)
	if len(got) != 1 || got[0].Outcome != db.TelemetryCandidateFieldUnused {
		t.Errorf("outcome = %v, want %q", got, db.TelemetryCandidateFieldUnused)
	}
}

// A key the database answered reached no plugin, so nothing consulted the
// proposal it was paid for.
func TestResolveWithPlugins_ADatabaseHitLeavesProposalsUnused(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("known-id", "STOCK", "XNAS", "USD", nil)

	got, err := ResolveWithPlugins(context.Background(), database, identifier.NewRegistry(), "", "", "",
		identifier.Identity{Proposed: []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if len(got.ProposalOutcomes) != 1 || got.ProposalOutcomes[0].Outcome != db.TelemetryCandidateFieldUnused {
		t.Errorf("ProposalOutcomes = %+v, want one unused", got.ProposalOutcomes)
	}
}

// --- Identity claims reaching the merge site (0139) ---

// Two plugins returning disjoint types are two claims, not one. This is the
// case adr/0060 is written for: the union of the two is a set the resolver
// assembled and nobody asserted, and telling it from one plugin naming both is
// what 0140 needs the partition for.
func TestResolveWithPlugins_DisjointResultsAreSeparateClaims(t *testing.T) {
	claims := claimsFromResolve(t,
		&fakePlugin{
			inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
			ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
		},
		&fakePlugin{
			inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple Inc."},
			ids:  []identifier.Identifier{{Type: "CUSIP", Value: "037833100"}},
		})

	if len(claims) != 2 {
		t.Fatalf("claims = %d, want 2: %+v", len(claims), claims)
	}
	if got := claims[0].Identifiers; len(got) != 1 || got[0].Ref.Type != "ISIN" || got[0].Role != db.ClaimRoleReturned {
		t.Errorf("claim 0 = %+v", got)
	}
	if got := claims[1].Identifiers; len(got) != 1 || got[0].Ref.Type != "CUSIP" || got[0].Role != db.ClaimRoleReturned {
		t.Errorf("claim 1 = %+v", got)
	}
}

// One plugin naming both is one claim. Same two identifiers as the test above,
// same flattened set, and the thing that differs is the only thing that says
// whether anything was asserted.
func TestResolveWithPlugins_OneResultNamingBothIsOneClaim(t *testing.T) {
	claims := claimsFromResolve(t,
		&fakePlugin{
			inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
			ids: []identifier.Identifier{
				{Type: "ISIN", Value: "US0378331005"},
				{Type: "CUSIP", Value: "037833100"},
			},
		},
		&fakePlugin{err: identifier.ErrNotIdentified})

	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1: %+v", len(claims), claims)
	}
	if len(claims[0].Identifiers) != 2 {
		t.Errorf("claim = %+v, want both identifiers together", claims[0].Identifiers)
	}
}

// A result discarded as inconsistent asserted nothing the resolver may act on,
// so it contributes no claim -- the same exclusion its identifiers already got.
func TestResolveWithPlugins_InconsistentResultContributesNoClaim(t *testing.T) {
	claims := claimsFromResolve(t,
		&fakePlugin{
			inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
			ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
		},
		&fakePlugin{
			inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "GBP", Name: "Something else"},
			ids:  []identifier.Identifier{{Type: "CUSIP", Value: "037833100"}},
		})

	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1: %+v", len(claims), claims)
	}
}

// A value the call was strictly filtered on reaches the merge site as part of
// the claim, and is not written onto the instrument. Carrying it is 0139;
// deciding what an admitted claim licenses is 0140.
func TestResolveWithPlugins_FilteredValueIsClaimedNotStored(t *testing.T) {
	var stored []db.IdentifierInput
	claims := claimsFromResolve(t,
		&fakePlugin{
			inst:     &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD", Name: "Apple"},
			ids:      []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
			filtered: []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
		},
		&fakePlugin{err: identifier.ErrNotIdentified},
		func(ids []db.IdentifierInput) { stored = ids })

	if len(claims) != 1 || len(claims[0].Identifiers) != 2 {
		t.Fatalf("claims = %+v, want one claim holding both", claims)
	}
	roles := map[string]string{}
	for _, c := range claims[0].Identifiers {
		roles[c.Ref.Type] = c.Role
	}
	if roles["OPENFIGI_SHARE_CLASS"] != db.ClaimRoleReturned || roles["ISIN"] != db.ClaimRoleFiltered {
		t.Errorf("roles = %v", roles)
	}
	for _, s := range stored {
		if s.Ref.Type == "ISIN" {
			t.Error("a filtered value was written onto the instrument")
		}
	}
}

// claimsFromResolve runs a two-plugin resolution and returns the claims that
// reached EnsureInstrument. The optional callback sees the identifiers stored
// alongside them.
func claimsFromResolve(t *testing.T, a, b identifier.Plugin, onStore ...func([]db.IdentifierInput)) []db.IdentityClaim {
	t.Helper()
	saved := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	defer func() { PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)

	var got []db.IdentityClaim
	database.EXPECT().
		EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, ids []db.IdentifierInput, claims []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			got = claims
			for _, f := range onStore {
				f(ids)
			}
			return "inst-1", nil
		})

	registry := identifier.NewRegistry()
	registry.Register("pluginA", a)
	registry.Register("pluginB", b)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}},
		false, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	return got
}

// --- a domain scopes an identifier rather than decorating it (0144) ---

// Two plugins naming one symbol on two venues have described two listings of a
// security, not one instrument twice. Both instruments are venue-silent here, so
// the venue comparison decides nothing and the identifiers have to.
func TestConsistentWith_OneSymbolOnTwoVenuesIsNotAgreement(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{AssetClass: "STOCK"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{AssetClass: "STOCK"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "AAPL"}},
	}
	if consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected two listings of one symbol to be inconsistent")
	}
}

// A segment MIC and the venue that operates it are one domain, per adr/0003, so
// the two results are talking about the same listing and agree.
func TestConsistentWith_ASegmentDomainAndItsOperatingMICAreOneSubject(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNGS", Value: "AAPL"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected a segment MIC and its operating MIC to be one subject")
	}
	o.ids = []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "MSFT"}}
	if consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected two symbols on one listing to be inconsistent")
	}
}

// A ticker naming no venue names no particular listing, so there is nothing to
// compare it against a venued one on. The venue disagreement, where there is
// one, is carried by the instruments rather than by the identifiers.
func TestConsistentWith_AnUndomainedTickerIsNotComparedAgainstAVenuedOne(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "MSFT"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected a bare ticker not to be compared against a venued one")
	}
}

// The two-listings rule is for types whose domain names a venue. A description's
// domain names the source that wrote it, so two sources describing one security
// are two names for it, however unalike the text.
func TestConsistentWith_TwoSourcesDescribingOneSecurityAgree(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: "ibkr", Value: "APPLE INC"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: "schwab", Value: "APPLE COMPUTER INC"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected two sources' descriptions of one security to agree")
	}
}

// A winner that named both listings has already said the security trades on
// each, so a result naming one of them agrees with it. Reading only the
// conflicting half would have the winner reject its own answer.
func TestConsistentWith_AWinnerNamingTwoListingsAcceptsEither(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{},
		ids: []identifier.Identifier{
			{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"},
			{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"},
		},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer(), nil) {
		t.Error("expected a result naming one of the winner's two listings to agree")
	}
}

// The venue is half of what the source stated. A result naming the symbol on
// another venue has agreed with the other half and with nothing that was said.
func TestConfirmedFields_AStatedTickerOnAnotherVenueIsNotConfirmed(t *testing.T) {
	inst := &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}}
	stated := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"}}
	ids := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"}}
	if got := confirmedFields(context.Background(), identifier.Hints{}, stated, inst, ids, testMICNormalizer()); len(got) != 0 {
		t.Errorf("confirmed = %v, want nothing: the venue stated is not the venue resolved", got)
	}
}

// A result that named no venue has not corroborated one, so a stated listing is
// not confirmed by a bare symbol coming back.
func TestConfirmedFields_AnUndomainedResolvedTickerDoesNotConfirmAStatedVenue(t *testing.T) {
	inst := &identifier.Instrument{AssetClass: "STOCK"}
	stated := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}
	ids := []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}
	if got := confirmedFields(context.Background(), identifier.Hints{}, stated, inst, ids, testMICNormalizer()); len(got) != 0 {
		t.Errorf("confirmed = %v, want nothing: a bare ticker corroborates no listing", got)
	}
}

// A source naming two listings is corroborated by the resolution landing on
// either, so every stated listing is consulted and not just the first.
func TestConfirmedFields_EveryStatedListingIsConsulted(t *testing.T) {
	inst := &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}}
	stated := []identifier.Identifier{
		{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"},
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"},
	}
	ids := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"}}
	got := confirmedFields(context.Background(), identifier.Hints{}, stated, inst, ids, testMICNormalizer())
	if len(got) != 2 || got[0] != "Exchange" || got[1] != "MIC_TICKER" {
		t.Errorf("confirmed = %v, want the venue and the ticker, each once", got)
	}
}

// The wrong venue is reported once, as the exchange difference it is. Saying it
// again as a MIC_TICKER row whose two values are the same string would be the
// same fact rendered as noise.
func TestCompareHints_ATickerOnAnotherVenueIsNotAValueDiff(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"}}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}}
	resolvedIDs := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, resolvedIDs, testMICNormalizer())
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %v", len(diffs), diffs)
	}
	if diffs[0].Field != "Exchange" {
		t.Errorf("unexpected diff: %+v", diffs[0])
	}
}

// A source that stated two listings is not contradicted by the resolution
// choosing the second of them.
func TestCompareHints_ASecondStatedListingMatches(t *testing.T) {
	idnHints := []identifier.Identifier{
		{Type: "MIC_TICKER", Domain: "XLON", Value: "VOD"},
		{Type: "MIC_TICKER", Domain: "XNAS", Value: "VOD"},
	}
	inst := &identifier.Instrument{Venue: identifier.Venue{MIC: "XNAS"}}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, testMICNormalizer())
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %v", diffs)
	}
}

// Winning the stated tier means a hint was checked and held up. A ticker under
// another domain was never compared, so there is nothing for the result to have
// held up against.
func TestResultMatchesHints_ATickerUnderAnotherDomainWasNotCompared(t *testing.T) {
	r := &pluginResult{
		inst: &identifier.Instrument{AssetClass: "STOCK"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	}
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XLON", Value: "AAPL"}}
	if resultMatchesHints(context.Background(), identifier.Hints{}, idnHints, r, testMICNormalizer()) {
		t.Error("expected no match: the ticker was never compared")
	}
}

// The propagation 0144 is about. The loser identified the same security and its
// name and CIK are facts about that security, so they fill the winner's blanks.
// Its currency is not: it named no venue, so it has not said which line the GBP
// belongs to, and the winner's line is on XNAS.
func TestResolveWithPlugins_AVenueSilentResultDoesNotSupplyTheCurrency(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).DoAndReturn(testMICCountry).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("venue", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Venue: identifier.Venue{MIC: "XNAS"}, Name: "Apple Inc"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	})
	registry.Register("security", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Currency: "GBP", CIK: "0000320193"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "venue", Precedence: 20}, {PluginID: "security", Precedence: 10}}, nil)
	// The CIK arrives, the currency does not, and the ISIN is merged either way:
	// the result was consistent, it just did not say where the GBP was measured.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "", "Apple Inc", "0000320193", "", gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, Attempt{}, nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
}
