package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	descpkg "github.com/leedenison/portfoliodb/server/identifier/description"
	"go.uber.org/mock/gomock"
)

// fakePlugin is a test double that returns fixed results.
type fakePlugin struct {
	inst *identifier.Instrument
	ids  []identifier.Identifier
	err  error
}

func (p *fakePlugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return p.inst, p.ids, p.err
}

func (p *fakePlugin) AcceptableSecurityTypes() map[string]bool { return nil }
func (p *fakePlugin) DefaultConfig() []byte                    { return nil }
func (p *fakePlugin) DisplayName() string                      { return "Fake" }

// tickerHintsCache builds an extractedHintsCache for tests where description
// extraction would have returned a TICKER hint with value equal to the
// instrument description.
func tickerHintsCache(source, desc string) map[string][]identifier.Identifier {
	return map[string][]identifier.Identifier{
		cacheKey(source, desc): {{Type: "TICKER", Domain: "", Value: desc}},
	}
}

func TestResolve_CacheHit_FromPrePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	cache := map[string]resolveResult{
		cacheKey(source, "AAPL"): {InstrumentID: "existing-id"},
	}

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", identifier.Hints{}, nil, cache, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "existing-id" {
		t.Errorf("InstrumentID = %q, want existing-id", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("unexpected IdErr: %+v", r.IdErr)
	}
}

// TestResolve_TickerOnlyFallback_ResolvesByTypeAndValue verifies that when the user supplies only a ticker (no exchange),
// the fallback lookup by (type, value) resolves to an instrument stored with a domain (e.g. TICKER+"US").
func TestResolve_TickerOnlyFallback_ResolvesByTypeAndValue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	// Exact (TICKER, "", "AAPL") misses because DB has (TICKER, "US", "AAPL").
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("", nil)
	// Fallback by (type, value) finds the instrument.
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "AAPL").
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "AAPL"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "fallback-id" {
		t.Errorf("InstrumentID = %q, want fallback-id", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("unexpected IdErr: %+v", r.IdErr)
	}
}

func TestResolve_CacheHit_NoPluginCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	cache := make(map[string]resolveResult)
	key := cacheKey(source, "GOOG")
	cache[key] = resolveResult{InstrumentID: "cached-id", FirstRowIndex: 0}

	// No DB or plugin calls when cache has entry
	r, err := Resolve(ctx, database, registry, "IBKR", source, "GOOG", identifier.Hints{}, nil, cache, 1, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "cached-id" {
		t.Errorf("InstrumentID = %q, want cached-id", r.InstrumentID)
	}
}

func TestResolve_NoExtractedHints_ExtractionFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	// nil extractedHintsCache → no hints → extraction failed path
	ctx := context.Background()
	source := "IBKR:test:statement"
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "UNKNOWN", Canonical: false}}, "", nil, nil).
		Return("broker-only-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "UNKNOWN", identifier.Hints{}, nil, nil, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "broker-only-id" {
		t.Errorf("InstrumentID = %q, want broker-only-id", r.InstrumentID)
	}
	if r.IdErr == nil {
		t.Fatal("expected IdErr for extraction failed")
	}
	if r.IdErr.Message != MsgExtractionFailed {
		t.Errorf("IdErr.Message = %q, want %q", r.IdErr.Message, MsgExtractionFailed)
	}
}

func TestResolve_AllPluginsErrNotIdentified_BrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	registry.Register("p1", &fakePlugin{err: identifier.ErrNotIdentified})

	ctx := context.Background()
	source := "IBKR:test:statement"
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "p1", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "UNKNOWN", Canonical: false}}, "", nil, nil).
		Return("broker-only-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "UNKNOWN", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "UNKNOWN"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgBrokerDescriptionOnly {
		t.Errorf("expected IdErr message %q, got %+v", MsgBrokerDescriptionOnly, r.IdErr)
	}
}

func TestResolve_OnePluginSuccess_EnsureInstrumentWithResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "AAPL"}, {Type: "ISIN", Value: "US0378331005"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time) (string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers (broker + ISIN), got %d", len(idns))
			}
			return "resolved-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "AAPL"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "resolved-id" {
		t.Errorf("InstrumentID = %q, want resolved-id", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("unexpected IdErr: %+v", r.IdErr)
	}
}

// TestResolve_BrokerDescriptionAlwaysStored verifies that when a plugin succeeds but does not return
// the (source, instrument_description) identifier, the resolver still adds it so future uploads can
// resolve by DB lookup without calling plugins again.
func TestResolve_BrokerDescriptionAlwaysStored(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	// Plugin returns only canonical ids; does not include (source, desc).
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "OPENFIGI_GLOBAL", Value: "BBG000B9XRY4"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", desc).
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", desc).
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time) (string, error) {
			hasSource := false
			for _, idn := range idns {
				if idn.Type == "BROKER_DESCRIPTION" && idn.Domain == source && idn.Value == desc {
					hasSource = true
					if idn.Canonical {
						t.Errorf("broker description identifier should be Canonical=false, got true")
					}
					break
				}
			}
			if !hasSource {
				t.Errorf("resolver must always store (source, instrument_description): missing identifier type=BROKER_DESCRIPTION domain=%q value=%q in %+v", source, desc, idns)
			}
			return "resolved-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, desc, identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, desc))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "resolved-id" {
		t.Errorf("InstrumentID = %q, want resolved-id", r.InstrumentID)
	}
}

func TestResolve_PluginReturnsUnderlying_ResolvesUnderlyingThenDerivative(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "AAPL  20250117C200"
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{
			AssetClass: "OPTION",
			Exchange:   "SMART",
			Currency:   "USD",
			Name:       "AAPL Call 20250117 200 C",
			UnderlyingIdentifiers: []identifier.Identifier{
				{Type: "TICKER", Value: "AAPL"},
			},
		},
		ids: []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: desc}, {Type: "CONID", Value: "12345"}},
		err: nil,
	})

	ctx := context.Background()
	// Top-level resolve: DB lookup for the option description.
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", desc).
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", desc).
		Return("", nil)
	// Top-level: list plugins.
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	// Recursive underlying resolution: DB lookup finds the underlying already exists.
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("underlying-uuid", nil)
	// Ensure derivative (OPTION) with underlying_id from recursive resolution.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", "SMART", "USD", "AAPL Call 20250117 200 C", gomock.Any(), gomock.Any(), gomock.Any(), "underlying-uuid", nil, nil).
		Return("option-uuid", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, desc, identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, desc))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "option-uuid" {
		t.Errorf("InstrumentID = %q, want option-uuid", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("unexpected IdErr: %+v", r.IdErr)
	}
}

func TestResolve_TwoPlugins_HigherPrecedenceWins(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "X"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "X"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "X").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "X").
		Return("", nil)
	// ListEnabledPluginConfigs returns precedence desc, so high (20) before low (10)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		Return("high-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "X", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "X"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "high-id" {
		t.Errorf("InstrumentID = %q, want high-id", r.InstrumentID)
	}
}

func TestResolve_TwoPlugins_MergedIdentifiersByPrecedence(t *testing.T) {
	// High-precedence plugin returns ISIN; low returns CUSIP. Both identifier types should appear (merged).
	// If both returned the same type (e.g. ISIN), high's value would be used.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Y"}, {Type: "CUSIP", Value: "12345"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Y"}, {Type: "ISIN", Value: "US0000000000"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "Y").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "Y").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time) (string, error) {
			// Merged: source from high first, ISIN from high, CUSIP from low (different types).
			types := make(map[string]string)
			for _, idn := range idns {
				types[idn.Type] = idn.Value
			}
			if types["BROKER_DESCRIPTION"] != "Y" || types["ISIN"] != "US0000000000" || types["CUSIP"] != "12345" {
				t.Errorf("merged identifiers: got %v, want BROKER_DESCRIPTION=Y, ISIN=US0000000000, CUSIP=12345", types)
			}
			return "merged-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, "Y", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "Y"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "merged-id" {
		t.Errorf("InstrumentID = %q, want merged-id", r.InstrumentID)
	}
}

func TestResolve_TwoPlugins_SameType_HighPrecedenceWins(t *testing.T) {
	// Both plugins return ISIN; high precedence value should be used.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Z"}, {Type: "ISIN", Value: "LOW-ISIN"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Z"}, {Type: "ISIN", Value: "HIGH-ISIN"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "Z").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "Z").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time) (string, error) {
			for _, idn := range idns {
				if idn.Type == "ISIN" && idn.Value != "HIGH-ISIN" {
					t.Errorf("same-type conflict: ISIN = %q, want HIGH-ISIN (high precedence)", idn.Value)
				}
			}
			return "id", nil
		})

	_, err := Resolve(ctx, database, registry, "IBKR", source, "Z", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "Z"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_PluginTimeout_FallbackAndMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	// Plugin that returns context.DeadlineExceeded (simulate timeout)
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "SLOW").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "SLOW").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "SLOW", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "SLOW", Canonical: false}}, "", nil, nil).
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "SLOW", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "SLOW"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgPluginTimeout {
		t.Errorf("expected IdErr message %q, got %+v", MsgPluginTimeout, r.IdErr)
	}
}

func TestResolve_PluginUnavailable_FallbackAndMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "BAD").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "BAD").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "BAD", "", "", []db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "BAD", Canonical: false}}, "", nil, nil).
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "BAD", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "BAD"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgPluginUnavailable {
		t.Errorf("expected IdErr message %q, got %+v", MsgPluginUnavailable, r.IdErr)
	}
}

// fakeDescPlugin is a test double for description.Plugin.
type fakeDescPlugin struct {
	acceptable map[string]bool
	results    map[string][]identifier.Identifier
	err        error
}

func (p *fakeDescPlugin) DisplayName() string              { return "FakeDesc" }
func (p *fakeDescPlugin) DefaultConfig() []byte            { return nil }
func (p *fakeDescPlugin) AcceptableSecurityTypes() map[string]bool { return p.acceptable }
func (p *fakeDescPlugin) ExtractBatch(_ context.Context, _ []byte, _, _ string, items []descpkg.BatchItem) (map[string][]identifier.Identifier, error) {
	if p.err != nil {
		return nil, p.err
	}
	out := make(map[string][]identifier.Identifier)
	for _, item := range items {
		if hints, ok := p.results[item.ID]; ok {
			out[item.ID] = hints
		}
	}
	return out, nil
}

// TestRunDescriptionPluginsBatch_MultiplePlugins_DifferentSecurityTypes verifies
// that when two description plugins handle disjoint security types, both get to
// process their respective items. Regression test for a bug where the first
// plugin returning any hints caused an early return, starving later plugins.
func TestRunDescriptionPluginsBatch_MultiplePlugins_DifferentSecurityTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	cashPlugin := &fakeDescPlugin{
		acceptable: map[string]bool{identifier.SecurityTypeHintCash: true},
		results: map[string][]identifier.Identifier{
			"cash-1": {{Type: "CURRENCY", Value: "USD"}},
		},
	}
	stockPlugin := &fakeDescPlugin{
		acceptable: map[string]bool{identifier.SecurityTypeHintStock: true},
		results: map[string][]identifier.Identifier{
			"stock-1": {{Type: "TICKER", Value: "AAPL"}},
		},
	}

	descRegistry := descpkg.NewRegistry()
	descRegistry.Register("cash", cashPlugin)
	descRegistry.Register("stock", stockPlugin)

	// Cash plugin has higher precedence (returned first by DESC ordering).
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryDescription).
		Return([]db.PluginConfigRow{
			{PluginID: "cash", Precedence: 2, Config: nil},
			{PluginID: "stock", Precedence: 1, Config: nil},
		}, nil)

	items := []descpkg.BatchItem{
		{ID: "cash-1", InstrumentDescription: "USD", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintCash}},
		{ID: "stock-1", InstrumentDescription: "AAPL APPLE INC", Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
	}

	got, err := runDescriptionPluginsBatch(context.Background(), database, descRegistry, nil, "broker", "source", items)
	if err != nil {
		t.Fatalf("runDescriptionPluginsBatch: %v", err)
	}

	if got == nil {
		t.Fatal("expected non-nil result map")
	}
	if hints, ok := got["cash-1"]; !ok || len(hints) == 0 {
		t.Errorf("cash-1: expected CURRENCY hint, got %v", hints)
	}
	if hints, ok := got["stock-1"]; !ok || len(hints) == 0 {
		t.Error("stock-1: expected TICKER hint, got nothing (stock plugin was never called)")
	}
}

func TestCacheKey(t *testing.T) {
	k := cacheKey("IBKR:test:statement", "AAPL")
	if k != "IBKR:test:statement\x00AAPL" {
		t.Errorf("cacheKey = %q, want IBKR:test:statement\\x00AAPL", k)
	}
}

func TestHintsByType(t *testing.T) {
	hints := []identifier.Identifier{
		{Type: "TICKER", Value: "EQQQ"},
		{Type: "ID_BB_GLOBAL_SHARE_CLASS_LEVEL", Value: "BBG123"},
		{Type: "TICKER", Value: "VUSA"},
	}
	ticker := hintsByType(hints, "TICKER")
	if len(ticker) != 2 || ticker[0].Value != "EQQQ" || ticker[1].Value != "VUSA" {
		t.Errorf("hintsByType(TICKER) = %+v; want two TICKER hints", ticker)
	}
	figi := hintsByType(hints, "ID_BB_GLOBAL_SHARE_CLASS_LEVEL")
	if len(figi) != 1 || figi[0].Value != "BBG123" {
		t.Errorf("hintsByType(ID_BB_GLOBAL_SHARE_CLASS_LEVEL) = %+v; want one hint", figi)
	}
	empty := hintsByType(hints, "ISIN")
	if len(empty) != 0 {
		t.Errorf("hintsByType(ISIN) = %+v; want empty", empty)
	}
}

// retryPlugin fails once with a non-ErrNotIdentified error, then succeeds on retry.
type retryPlugin struct {
	callCount int
	inst      *identifier.Instrument
	ids       []identifier.Identifier
}

func (p *retryPlugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, hints identifier.Hints, identifierHints []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	p.callCount++
	if p.callCount == 1 {
		return nil, nil, errors.New("temporary failure")
	}
	return p.inst, p.ids, nil
}

func (p *retryPlugin) AcceptableSecurityTypes() map[string]bool { return nil }
func (p *retryPlugin) DefaultConfig() []byte                    { return nil }
func (p *retryPlugin) DisplayName() string                      { return "Retry" }

func TestResolve_PluginFailsThenRetrySucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("retry", &retryPlugin{
		inst: &identifier.Instrument{Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "RETRY"}},
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "RETRY").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "RETRY").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "retry", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "Retried", gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil).
		Return("retried-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "RETRY", identifier.Hints{}, nil, nil, 0, nil, tickerHintsCache(source, "RETRY"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "retried-id" {
		t.Errorf("InstrumentID = %q, want retried-id", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("unexpected IdErr after retry success: %+v", r.IdErr)
	}
}
