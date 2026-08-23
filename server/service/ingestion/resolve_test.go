package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	candpkg "github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"go.uber.org/mock/gomock"
)

// fakePlugin is a test double that returns fixed results.
type fakePlugin struct {
	inst *identifier.Instrument
	ids  []identifier.Identifier
	err  error
}

func (p *fakePlugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident identifier.Identity) (identifier.Result, error) {
	hints, identifierHints := ident.Hints, ident.Stated
	_, _ = hints, identifierHints
	if ctx.Err() != nil {
		return identifier.Result{}, ctx.Err()
	}
	return identifier.Result{Instrument: p.inst, Identifiers: p.ids}, p.err
}

func (p *fakePlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *fakePlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *fakePlugin) DefaultConfig() []byte                      { return nil }
func (p *fakePlugin) DisplayName() string                        { return "Fake" }

// stockHints is what a real posting carries alongside its description: the asset
// class its source stated. A Path B resolution needs it, because the ticker it
// resolves by was proposed rather than stated, and something nobody guessed has
// to corroborate the result before it is kept. See adr/0059.
var stockHints = identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}

// tickerHintsCache builds an extractedHintsCache for tests where description
// extraction would have returned a TICKER hint with value equal to the
// instrument description.
func tickerHintsCache(source, desc string) map[string]keyProposals {
	return map[string]keyProposals{
		cacheKey(source, desc): {
			// A call id, because a proposal that reached the resolver came from
			// one and telemetry writes no field row without it.
			CallID: "call-1",
			Proposals: []candpkg.Proposal{{
				Field:      candpkg.FieldTicker,
				Identifier: identifier.Identifier{Type: "MIC_TICKER", Domain: "", Value: desc},
				Confidence: 0.9,
			}},
		},
	}
}

func TestResolve_CacheHit_FromPrePass(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	cache := map[string]resolveResult{
		cacheKey(source, "AAPL"): {InstrumentID: "existing-id"},
	}

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", identifier.Hints{}, nil, prePass{resolved: cache, conflicts: nil, proposed: nil}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	// Exact (TICKER, "", "AAPL") misses because DB has (TICKER, "US", "AAPL").
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	// Fallback by (type, value) finds the instrument.
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "AAPL")}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	cache := make(map[string]resolveResult)
	key := cacheKey(source, "GOOG")
	cache[key] = resolveResult{InstrumentID: "cached-id", FirstRowIndex: 0}

	// No DB or plugin calls when cache has entry
	r, err := Resolve(ctx, database, registry, "IBKR", source, "GOOG", identifier.Hints{}, nil, prePass{resolved: cache, conflicts: nil, proposed: nil}, 1, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	// nil extractedHintsCache → no hints → extraction failed path
	ctx := context.Background()
	source := "IBKR:test:statement"
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "UNKNOWN", Domain: source},
			Canonical: false,
		}}, gomock.Any(), "", nil, nil, nil).
		Return("broker-only-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "UNKNOWN", identifier.Hints{}, nil, prePass{resolved: nil, conflicts: nil, proposed: nil}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("p1", &fakePlugin{err: identifier.ErrNotIdentified})

	ctx := context.Background()
	source := "IBKR:test:statement"
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "UNKNOWN").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "UNKNOWN").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "UNKNOWN").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "p1", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "UNKNOWN", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "UNKNOWN", Domain: source},
			Canonical: false,
		}}, gomock.Any(), "", nil, nil, nil).
		Return("broker-only-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "UNKNOWN", identifier.Hints{}, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "UNKNOWN")}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple Inc.", Listing: identifier.Listing{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "AAPL"}, {Type: "ISIN", Value: "US0378331005"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers (broker + ISIN), got %d", len(idns))
			}
			return "resolved-id", "listing-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, "AAPL", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "AAPL")}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	// Plugin returns only canonical ids; does not include (source, desc).
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple Inc.", Listing: identifier.Listing{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", desc).
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", desc).
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), desc).Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			hasSource := false
			for _, idn := range idns {
				if idn.Ref.Type == "BROKER_DESCRIPTION" && idn.Ref.Domain == source && idn.Ref.Value == desc {
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
			return "resolved-id", "listing-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, desc, stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, desc)}, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "resolved-id" {
		t.Errorf("InstrumentID = %q, want resolved-id", r.InstrumentID)
	}
}

// pathATestDB is the mock a Path A resolution needs: the stated ISIN names
// nothing, one plugin answers, and the exchange lookups are incidental.
func pathATestDB(t *testing.T, ctrl *gomock.Controller) (*mock.MockDB, *identifier.Registry) {
	t.Helper()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").Return("", nil)
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10}}, nil)
	registry := identifier.NewRegistry()
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple Inc.", Listing: identifier.Listing{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	})
	return database, registry
}

// hasBrokerDescription reports whether the identifiers an instrument was ensured
// with name the description under its source.
func hasBrokerDescription(idns []db.IdentifierInput, source, desc string) bool {
	for _, idn := range idns {
		if idn.Ref.Type == "BROKER_DESCRIPTION" && idn.Ref.Domain == source && idn.Ref.Value == desc {
			return true
		}
	}
	return false
}

// TestResolve_PathABindsToTheInstrumentTheDescriptionAlreadyNames is issue 0135.
// A description already held by an instrument nothing has identified must reach
// that instrument when a later upload carries identifiers. Naming the description
// among the identifiers is how: the row is already stored, so EnsureInstrument
// matches it rather than minting a second instrument beside the one already
// holding this description's transactions.
func TestResolve_PathABindsToTheInstrumentTheDescriptionAlreadyNames(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	database, registry := pathATestDB(t, ctrl)

	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			if !hasBrokerDescription(idns, source, desc) {
				t.Errorf("the description was not named, so this ensure mints a second instrument beside %q: %+v", "desc-only-id", idns)
			}
			return "desc-only-id", "listing-id", nil
		})

	hints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	pre := prePass{descOnly: map[string]string{cacheKeyWithHints(source, desc, hints): "desc-only-id"}}
	r, err := Resolve(context.Background(), database, registry, "IBKR", source, desc, stockHints, hints, pre, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "desc-only-id" {
		t.Errorf("InstrumentID = %q, want the instrument the description already named", r.InstrumentID)
	}
}

// TestResolve_PathAStoresNoDescriptionWhereNoneIsHeld is the other half of the
// same rule. Where the description names nothing -- or names an instrument that
// already has an identity, which the pre-pass declines to report -- no mapping is
// minted: the client's identifiers are authoritative and a description-derived
// one would pollute later lookups. See adr/0004; whether it should be stored is
// 0106's question.
func TestResolve_PathAStoresNoDescriptionWhereNoneIsHeld(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	database, registry := pathATestDB(t, ctrl)

	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			if hasBrokerDescription(idns, source, desc) {
				t.Errorf("(source, description) minted on the hinted path: %+v", idns)
			}
			return "resolved-id", "listing-id", nil
		})

	hints := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	r, err := Resolve(context.Background(), database, registry, "IBKR", source, desc, stockHints, hints, prePass{}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "AAPL  20250117C200"
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{
			AssetClass: "OPTION",
			Name:       "AAPL Call 20250117 200 C",
			Listing: identifier.Listing{
				Venue:    identifier.Venue{MIC: "SMART"},
				Currency: "USD",
			},
			UnderlyingIdentifiers: []identifier.Identifier{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
		},
		ids: []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: desc}, {Type: "CONID", Value: "12345"}},
		err: nil,
	})

	ctx := context.Background()
	// Top-level resolve: DB lookup for the option description.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", desc).
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", desc).
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), desc).Return("", nil).AnyTimes()
	// Top-level: list plugins.
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	// Recursive underlying resolution: DB lookup finds the underlying already exists.
	// The underlying hint from plugin uses MIC_TICKER with empty domain.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("underlying-uuid", "", "", "", nil)
	// The option states USD, which names the line of AAPL it delivers.
	database.EXPECT().EnsureListing(gomock.Any(), "underlying-uuid", "USD").Return("underlying-line-uuid", nil)
	// Ensure derivative (OPTION) with underlying_id from recursive resolution.
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "OPTION", "SMART", "USD", "AAPL Call 20250117 200 C", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "underlying-line-uuid", nil, nil, nil).
		Return("option-uuid", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, desc, identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintOption}, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, desc)}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "X"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "X"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "X").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "X").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "X").Return("", nil).AnyTimes()
	// ListEnabledPluginConfigs returns precedence desc, so high (20) before low (10)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("high-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "X", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "X")}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Y"}, {Type: "CUSIP", Value: "12345"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Y"}, {Type: "ISIN", Value: "US0000000000"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "Y").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "Y").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "Y").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			// Merged: source from high first, ISIN from high, CUSIP from low (different types).
			types := make(map[string]string)
			for _, idn := range idns {
				types[idn.Ref.Type] = idn.Ref.Value
			}
			if types["BROKER_DESCRIPTION"] != "Y" || types["ISIN"] != "US0000000000" || types["CUSIP"] != "12345" {
				t.Errorf("merged identifiers: got %v, want BROKER_DESCRIPTION=Y, ISIN=US0000000000, CUSIP=12345", types)
			}
			return "merged-id", "listing-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", source, "Y", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "Y")}, 0, nil, nil)
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
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Low"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Z"}, {Type: "ISIN", Value: "LOW-ISIN"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "High"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "Z"}, {Type: "ISIN", Value: "HIGH-ISIN"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "Z").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "Z").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "Z").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "High", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _, _ *time.Time, _ *db.OptionFields) (string, string, error) {
			for _, idn := range idns {
				if idn.Ref.Type == "ISIN" && idn.Ref.Value != "HIGH-ISIN" {
					t.Errorf("same-type conflict: ISIN = %q, want HIGH-ISIN (high precedence)", idn.Ref.Value)
				}
			}
			return "id", "listing-id", nil
		})

	_, err := Resolve(ctx, database, registry, "IBKR", source, "Z", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "Z")}, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_PluginTimeout_FallbackAndMessage(t *testing.T) {
	saved := identification.PluginRetryBackoff
	identification.PluginRetryBackoff = time.Millisecond
	defer func() { identification.PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	// Plugin that returns context.DeadlineExceeded (simulate timeout)
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "SLOW").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "SLOW").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "SLOW").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "SLOW", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "SLOW", Domain: source},
			Canonical: false,
		}}, gomock.Any(), "", nil, nil, nil).
		Return("fallback-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "SLOW", identifier.Hints{}, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "SLOW")}, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgPluginTimeout {
		t.Errorf("expected IdErr message %q, got %+v", MsgPluginTimeout, r.IdErr)
	}
}

func TestResolve_PluginUnavailable_FallbackAndMessage(t *testing.T) {
	saved := identification.PluginRetryBackoff
	identification.PluginRetryBackoff = time.Millisecond
	defer func() { identification.PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BAD").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BAD").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "BAD").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "BAD", "", "", []db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "BAD", Domain: source},
			Canonical: false,
		}}, gomock.Any(), "", nil, nil, nil).
		Return("fallback-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "BAD", identifier.Hints{}, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "BAD")}, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgPluginUnavailable {
		t.Errorf("expected IdErr message %q, got %+v", MsgPluginUnavailable, r.IdErr)
	}
}

// fakeDescPlugin is a test double for candidate.Plugin.
type fakeDescPlugin struct {
	acceptableKinds map[string]bool
	acceptable      map[string]bool
	results         map[string][]identifier.Identifier
	// resultsByDesc answers by instrument description rather than by batch id,
	// for a caller that reaches the plugin through proposeCandidates and so does
	// not know the id it hashed the key to.
	resultsByDesc map[string][]identifier.Identifier
	// seen is every item the plugin was handed, in the order it got them.
	seen []candpkg.BatchItem
	err  error
	// tokens is what the plugin reports the call cost. Nil is a plugin that costs
	// nothing to call, which is what keeps the token columns null for it.
	tokens *candpkg.Usage
}

func (p *fakeDescPlugin) DisplayName() string                        { return "FakeDesc" }
func (p *fakeDescPlugin) DefaultConfig() []byte                      { return nil }
func (p *fakeDescPlugin) AcceptableInstrumentKinds() map[string]bool { return p.acceptableKinds }
func (p *fakeDescPlugin) AcceptableSecurityTypes() map[string]bool   { return p.acceptable }
func (p *fakeDescPlugin) ProposeBatch(_ context.Context, _ []byte, _, _ string, items []candpkg.BatchItem) (candpkg.Result, error) {
	if p.err != nil {
		return candpkg.Result{Telemetry: candpkg.Telemetry{Outcome: candpkg.OutcomeError, Tokens: p.tokens}}, p.err
	}
	p.seen = append(p.seen, items...)
	out := make(map[string][]candpkg.Proposal)
	for _, item := range items {
		hints, ok := p.results[item.ID]
		if !ok {
			hints, ok = p.resultsByDesc[item.InstrumentDescription]
		}
		if ok {
			ps := make([]candpkg.Proposal, 0, len(hints))
			for _, h := range hints {
				ps = append(ps, candpkg.Proposal{Field: candpkg.FieldTicker, Identifier: h})
			}
			out[item.ID] = ps
		}
	}
	outcome := candpkg.OutcomeNoHints
	if len(out) > 0 {
		outcome = candpkg.OutcomeHintsReturned
	}
	return candpkg.Result{Proposed: out, Telemetry: candpkg.Telemetry{Outcome: outcome, Tokens: p.tokens}}, nil
}

// TestRunDescriptionPluginsBatch_MultiplePlugins_DifferentSecurityTypes verifies
// that when two candidate plugins handle disjoint security types, both get to
// process their respective items. Regression test for a bug where the first
// plugin returning any hints caused an early return, starving later plugins.
func TestRunDescriptionPluginsBatch_MultiplePlugins_DifferentSecurityTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	cashPlugin := &fakeDescPlugin{
		acceptableKinds: map[string]bool{identifier.InstrumentKindCash: true},
		acceptable:      map[string]bool{identifier.SecurityTypeHintCash: true},
		results: map[string][]identifier.Identifier{
			"cash-1": {{Type: "CURRENCY", Value: "USD"}},
		},
	}
	stockPlugin := &fakeDescPlugin{
		acceptableKinds: map[string]bool{identifier.InstrumentKindSecurity: true},
		acceptable:      map[string]bool{identifier.SecurityTypeHintStock: true},
		results: map[string][]identifier.Identifier{
			"stock-1": {{Type: "MIC_TICKER", Value: "AAPL"}},
		},
	}

	candRegistry := candpkg.NewRegistry()
	candRegistry.Register("cash", cashPlugin)
	candRegistry.Register("stock", stockPlugin)

	// Cash plugin has higher precedence (returned first by DESC ordering).
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCandidate).
		Return([]db.PluginConfigRow{
			{PluginID: "cash", Precedence: 2, Config: nil},
			{PluginID: "stock", Precedence: 1, Config: nil},
		}, nil)

	items := []candpkg.BatchItem{
		{ID: "cash-1", InstrumentDescription: "USD", Hints: identifier.Hints{InstrumentKind: identifier.InstrumentKindCash, SecurityTypeHint: identifier.SecurityTypeHintCash}},
		{ID: "stock-1", InstrumentDescription: "AAPL APPLE INC", Hints: identifier.Hints{InstrumentKind: identifier.InstrumentKindSecurity, SecurityTypeHint: identifier.SecurityTypeHintStock}},
	}

	got, _, err := runCandidatePluginsBatch(context.Background(), ingestDeps{DB: database, CandidateRegistry: candRegistry}, "broker", "source", items)
	if err != nil {
		t.Fatalf("runCandidatePluginsBatch: %v", err)
	}

	if got == nil {
		t.Fatal("expected non-nil result map")
	}
	if hints, ok := got["cash-1"]; !ok || len(hints.Proposals) == 0 {
		t.Errorf("cash-1: expected CURRENCY hint, got %v", hints)
	}
	if hints, ok := got["stock-1"]; !ok || len(hints.Proposals) == 0 {
		t.Error("stock-1: expected MIC_TICKER hint, got nothing (stock plugin was never called)")
	}
}

// TestRunDescriptionPluginsBatch_TransferSkipsCash verifies that a TRANSFER
// transaction (kind=SECURITY, type=UNKNOWN) is routed to security plugins
// but not to cash plugins.
func TestRunDescriptionPluginsBatch_TransferSkipsCash(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	cashPlugin := &fakeDescPlugin{
		acceptableKinds: map[string]bool{identifier.InstrumentKindCash: true},
		acceptable:      map[string]bool{identifier.SecurityTypeHintCash: true},
		results: map[string][]identifier.Identifier{
			"t-1": {{Type: "CURRENCY", Value: "USD"}},
		},
	}
	stockPlugin := &fakeDescPlugin{
		acceptableKinds: map[string]bool{identifier.InstrumentKindSecurity: true},
		acceptable:      map[string]bool{identifier.SecurityTypeHintStock: true},
		results: map[string][]identifier.Identifier{
			"t-1": {{Type: "MIC_TICKER", Value: "ABNB"}},
		},
	}

	candRegistry := candpkg.NewRegistry()
	candRegistry.Register("cash", cashPlugin)
	candRegistry.Register("stock", stockPlugin)

	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCandidate).
		Return([]db.PluginConfigRow{
			{PluginID: "cash", Precedence: 2, Config: nil},
			{PluginID: "stock", Precedence: 1, Config: nil},
		}, nil)

	// TRANSFER: kind=SECURITY, type=UNKNOWN
	items := []candpkg.BatchItem{
		{ID: "t-1", InstrumentDescription: "ABNB", Hints: identifier.Hints{
			InstrumentKind:   identifier.InstrumentKindSecurity,
			SecurityTypeHint: identifier.SecurityTypeHintUnknown,
			Currency:         "USD",
		}},
	}

	got, _, err := runCandidatePluginsBatch(context.Background(), ingestDeps{DB: database, CandidateRegistry: candRegistry}, "broker", "source", items)
	if err != nil {
		t.Fatalf("runCandidatePluginsBatch: %v", err)
	}

	if got == nil {
		t.Fatal("expected non-nil result map")
	}
	kp, ok := got["t-1"]
	if !ok || len(kp.Proposals) == 0 {
		t.Fatal("t-1: expected hints from stock plugin, got nothing")
	}
	first := kp.Proposals[0].Identifier
	if first.Type == "CURRENCY" {
		t.Error("t-1: TRANSFER should NOT be routed to cash plugin, but got CURRENCY hint")
	}
	if first.Type != "MIC_TICKER" || first.Value != "ABNB" {
		t.Errorf("t-1: expected MIC_TICKER=ABNB from stock plugin, got %+v", kp.Proposals)
	}
}

func TestCacheKey(t *testing.T) {
	k := cacheKey("IBKR:test:statement", "AAPL")
	if k != "IBKR:test:statement\x00AAPL" {
		t.Errorf("cacheKey = %q, want IBKR:test:statement\\x00AAPL", k)
	}
}

func TestCacheKeyWithHints_NoHints(t *testing.T) {
	k := cacheKeyWithHints("IBKR:test:statement", "AAPL", nil)
	want := cacheKey("IBKR:test:statement", "AAPL")
	if k != want {
		t.Errorf("cacheKeyWithHints(nil hints) = %q, want %q", k, want)
	}
}

func TestCacheKeyWithHints_OrderIndependent(t *testing.T) {
	source := "IBKR:test:statement"
	desc := "AAPL"
	k1 := cacheKeyWithHints(source, desc, []identifier.Identifier{
		{Type: "CUSIP", Value: "037833100"},
		{Type: "ISIN", Value: "US0378331005"},
	})
	k2 := cacheKeyWithHints(source, desc, []identifier.Identifier{
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "CUSIP", Value: "037833100"},
	})
	if k1 != k2 {
		t.Errorf("cache keys should be equal regardless of hint order:\n  k1 = %q\n  k2 = %q", k1, k2)
	}
}

func TestCacheKeyWithHints_DifferentHintsDifferentKeys(t *testing.T) {
	source := "IBKR:test:statement"
	desc := "MSFT MICROSOFT CORP"
	cusipKey := cacheKeyWithHints(source, desc, []identifier.Identifier{
		{Type: "CUSIP", Value: "594918104"},
	})
	currencyKey := cacheKeyWithHints(source, desc, []identifier.Identifier{
		{Type: "CURRENCY", Value: "USD"},
	})
	if cusipKey == currencyKey {
		t.Errorf("cache keys should differ for different hints on the same description, both = %q", cusipKey)
	}
}

// TestResolve_SameDescription_DifferentHints_NoCacheConflict verifies that a
// security transaction and a cash transaction with the same instrument
// description but different identifier hints resolve independently without
// a cache conflict error.  This is the scenario where e.g. a stock buy for
// "MSFT MICROSOFT CORP" (CUSIP hint) and a cash trade for "MSFT MICROSOFT CORP"
// (CURRENCY hint) appear in the same batch.
// The cache key carries the stated identifiers, so a security posting and the
// cash leg beside it -- same description, different hints -- resolve
// independently rather than the first answer standing for both.
//
// The database lookups themselves belong to proposeCandidates now, so this seeds
// the cache the way the pre-pass would and tests the key derivation alone, which
// is what the property is about.
func TestResolve_SameDescription_DifferentHints_NoCacheConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	source := "IBKR:test:statement"
	desc := "MSFT MICROSOFT CORP"
	securityHints := []identifier.Identifier{{Type: "CUSIP", Value: "594918104"}}
	cashHints := []identifier.Identifier{{Type: "CURRENCY", Value: "USD"}}

	cache := map[string]resolveResult{
		cacheKeyWithHints(source, desc, securityHints): {InstrumentID: "msft-inst-id", DBHitOutcome: db.TelemetryResolutionDBIdentifierHints},
		cacheKeyWithHints(source, desc, cashHints):     {InstrumentID: "usd-inst-id", DBHitOutcome: db.TelemetryResolutionDBIdentifierHints},
	}

	r1, err := Resolve(ctx, database, registry, "IBKR", source, desc,
		identifier.Hints{}, securityHints, prePass{resolved: cache, conflicts: nil, proposed: nil}, 0, nil, nil)
	if err != nil {
		t.Fatalf("Resolve (security): %v", err)
	}
	if r1.InstrumentID != "msft-inst-id" {
		t.Errorf("security InstrumentID = %q, want msft-inst-id", r1.InstrumentID)
	}

	r2, err := Resolve(ctx, database, registry, "IBKR", source, desc,
		identifier.Hints{}, cashHints, prePass{resolved: cache, conflicts: nil, proposed: nil}, 1, nil, nil)
	if err != nil {
		t.Fatalf("Resolve (cash): %v", err)
	}
	if r2.InstrumentID != "usd-inst-id" {
		t.Errorf("cash InstrumentID = %q, want usd-inst-id", r2.InstrumentID)
	}
}

// A key the pre-pass found to resolve to more than one instrument is raised at
// the row that carries it, where the row index is known -- the pre-pass records
// it rather than raising, so one bad key does not stop the lookups for the rest.
func TestResolve_ConflictingHintsFromThePrePassAreRaised(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	source := "IBKR:test:statement"
	desc := "AMBIGUOUS"
	hints := []identifier.Identifier{{Type: "ISIN", Value: "US0000000001"}}
	conflicts := map[string]bool{cacheKeyWithHints(source, desc, hints): true}

	_, err := Resolve(context.Background(), database, registry, "IBKR", source, desc,
		identifier.Hints{}, hints, prePass{resolved: map[string]resolveResult{}, conflicts: conflicts, proposed: nil}, 0, nil, nil)
	if err == nil {
		t.Fatal("expected an error for conflicting identifier hints")
	}
}

func TestHintsByType(t *testing.T) {
	hints := []identifier.Identifier{
		{Type: "MIC_TICKER", Value: "EQQQ"},
		{Type: "ID_BB_GLOBAL_SHARE_CLASS_LEVEL", Value: "BBG123"},
		{Type: "MIC_TICKER", Value: "VUSA"},
	}
	ticker := hintsByType(hints, "MIC_TICKER")
	if len(ticker) != 2 || ticker[0].Value != "EQQQ" || ticker[1].Value != "VUSA" {
		t.Errorf("hintsByType(MIC_TICKER) = %+v; want two MIC_TICKER hints", ticker)
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

func (p *retryPlugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident identifier.Identity) (identifier.Result, error) {
	hints, identifierHints := ident.Hints, ident.Stated
	_, _ = hints, identifierHints
	p.callCount++
	if p.callCount == 1 {
		return identifier.Result{}, errors.New("temporary failure")
	}
	return identifier.Result{Instrument: p.inst, Identifiers: p.ids}, nil
}

func (p *retryPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *retryPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *retryPlugin) DefaultConfig() []byte                      { return nil }
func (p *retryPlugin) DisplayName() string                        { return "Retry" }

func TestResolve_PluginFailsThenRetrySucceeds(t *testing.T) {
	saved := identification.PluginRetryBackoff
	identification.PluginRetryBackoff = time.Millisecond
	defer func() { identification.PluginRetryBackoff = saved }()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	registry.Register("retry", &retryPlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: source, Value: "RETRY"}},
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "RETRY").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "RETRY").
		Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "RETRY").Return("", nil).AnyTimes()
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "retry", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Retried", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "", nil, nil, nil).
		Return("retried-id", "listing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", source, "RETRY", stockHints, nil, prePass{resolved: nil, conflicts: nil, proposed: tickerHintsCache(source, "RETRY")}, 0, nil, nil)
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

// The line falls at the venue: a source that named one has said the last thing
// that changes which listing resolution lands on, and everything else has left a
// choice open no provider lookup closes.
func TestIdentityComplete(t *testing.T) {
	cases := []struct {
		name   string
		stated []identifier.Identifier
		want   bool
	}{
		{"nothing stated", nil, false},
		{"a bare ticker", []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, false},
		{"a ticker with its MIC", []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}}, true},
		{"an ISIN alone", []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}, false},
		{"a CUSIP alone", []identifier.Identifier{{Type: "CUSIP", Value: "037833100"}}, false},
		// Issued per market and per line, so it names the line without a domain
		// to say which.
		{"a SEDOL alone", []identifier.Identifier{{Type: "SEDOL", Value: "2046251"}}, true},
		{"a composite FIGI alone", []identifier.Identifier{{Type: "OPENFIGI_COMPOSITE", Value: "BBG000B9XRY4"}}, true},
		{"a broker description", []identifier.Identifier{{Type: "BROKER_DESCRIPTION", Domain: "SRC", Value: "APPLE INC"}}, false},
		{"a currency", []identifier.Identifier{{Type: "CURRENCY", Value: "USD"}}, true},
		{"an FX pair", []identifier.Identifier{{Type: "FX_PAIR", Value: "GBPUSD"}}, true},
		{"a contract symbol", []identifier.Identifier{{Type: "OCC", Value: "AAPL  251219C00200000"}}, true},
		{"a share class FIGI", []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8"}}, true},
		{"an ISIN and a venue-qualified ticker", []identifier.Identifier{
			{Type: "ISIN", Value: "US0378331005"},
			{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
		}, true},
		{"an ISIN and a bare ticker", []identifier.Identifier{
			{Type: "ISIN", Value: "US0378331005"},
			{Type: "MIC_TICKER", Value: "AAPL"},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := identityComplete(c.stated); got != c.want {
				t.Errorf("identityComplete(%v) = %v, want %v", c.stated, got, c.want)
			}
		})
	}
}

// capturingPlugin records the identity it was called with, so a test can assert
// which provenance a value arrived under rather than only that it arrived.
type capturingPlugin struct {
	fakePlugin
	got identifier.Identity
}

func (p *capturingPlugin) Identify(ctx context.Context, config []byte, broker, source, instrumentDescription string, ident identifier.Identity) (identifier.Result, error) {
	p.got = ident
	return p.fakePlugin.Identify(ctx, config, broker, source, instrumentDescription, ident)
}

// A proposal made for a key the source stated identifiers for reaches the
// identifier plugins as a proposal. Path A used to drop it: the stage never ran
// for a hinted key, so there was nothing to pass, and passing it as a stated
// identifier would let a guess be queried and stored. See adr/0057.
func TestResolve_PathAPassesProposalsApartFromWhatTheSourceStated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "ISIN", "US0378331005").Return("", nil)
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "p", Precedence: 1}}, nil)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("aapl-id", "listing-id", nil)
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	plugin := &capturingPlugin{fakePlugin: fakePlugin{
		inst: &identifier.Instrument{AssetClass: db.AssetClassStock, Listing: identifier.Listing{Venue: identifier.Venue{MIC: "XNAS"}, Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}},
	}}
	registry := identifier.NewRegistry()
	registry.Register("p", plugin)

	stated := []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}
	key := cacheKeyWithHints("SRC", "APPLE INC", stated)
	pre := prePass{
		resolved:  map[string]resolveResult{},
		conflicts: map[string]bool{},
		proposed: map[string]keyProposals{key: {CallID: "call-1", Proposals: []candpkg.Proposal{{
			Field:      candpkg.FieldExchange,
			Identifier: identifier.Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"},
			Confidence: 0.7,
		}}}},
	}

	if _, err := Resolve(context.Background(), database, registry, "IBKR", "SRC", "APPLE INC",
		identifier.Hints{Currency: "USD", SecurityTypeHint: identifier.SecurityTypeHintStock},
		stated, pre, 0, nil, nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(plugin.got.Stated) != 1 || plugin.got.Stated[0].Type != "ISIN" {
		t.Errorf("Stated = %v, want the source's ISIN alone", plugin.got.Stated)
	}
	if len(plugin.got.Proposed) != 1 || plugin.got.Proposed[0].Domain != "XNAS" {
		t.Errorf("Proposed = %v, want the proposed venue", plugin.got.Proposed)
	}
}

// --- an invented identifier round-trips before it is trusted (0132) ---

// roundTripResolve resolves a description-only posting whose ticker the
// candidate stage proposed, against one plugin returning inst.
func roundTripResolve(t *testing.T, hints identifier.Hints, inst *identifier.Instrument, ensureName string) (resolveResult, error) {
	t.Helper()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().LookupMICCountry(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", "", "", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), gomock.Any()).Return("", nil).AnyTimes()

	source := "IBKR:test:statement"
	registry := identifier.NewRegistry()
	registry.Register("p", &fakePlugin{
		inst: inst,
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "GUESS"}},
	})
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "p", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), ensureName, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("out-id", "listing-id", nil)

	return Resolve(context.Background(), database, registry, "IBKR", source, "GUESS",
		hints, nil, prePass{proposed: tickerHintsCache(source, "GUESS")}, 0, nil, nil)
}

// A guessed key that resolves to something agreeing with what the source stated
// is kept. The source said STOCK and the provider said STOCK, so the ticker the
// candidate stage invented has round-tripped through something nobody guessed.
func TestResolve_ProposedKeyConfirmedBySecurityTypeIsKept(t *testing.T) {
	r, err := roundTripResolve(t,
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		&identifier.Instrument{AssetClass: "STOCK", Name: "Real Co"},
		"Real Co")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "out-id" {
		t.Errorf("InstrumentID = %q, want out-id", r.InstrumentID)
	}
	if r.IdErr != nil {
		t.Errorf("IdErr = %+v, want none", r.IdErr)
	}
}

// A guessed key that resolves to something nothing independent agrees with is
// dropped, and the description is bound to a broker-description-only instrument
// instead. The provider answering proves the ticker names some security, not
// that it names this one.
func TestResolve_ProposedKeyConfirmingNothingIsDropped(t *testing.T) {
	r, err := roundTripResolve(t,
		identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		// Says nothing the source can be checked against.
		&identifier.Instrument{Name: "Says nothing"},
		// The fallback ensures the description itself, not the plugin's name.
		"GUESS")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "out-id" {
		t.Errorf("InstrumentID = %q, want the broker-description-only instrument", r.InstrumentID)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgProposalUnconfirmed {
		t.Fatalf("IdErr = %+v, want %q", r.IdErr, MsgProposalUnconfirmed)
	}
}

// The source stating nothing to check against is not a pass. There is then no
// way to tell an invented ticker from a real one, and the guess is dropped for
// the same reason.
func TestResolve_ProposedKeyWithNothingToCheckAgainstIsDropped(t *testing.T) {
	r, err := roundTripResolve(t,
		identifier.Hints{},
		&identifier.Instrument{AssetClass: "STOCK", Name: "Real Co"},
		"GUESS")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgProposalUnconfirmed {
		t.Fatalf("IdErr = %+v, want %q", r.IdErr, MsgProposalUnconfirmed)
	}
	if r.InstrumentID != "out-id" {
		t.Errorf("InstrumentID = %q, want the broker-description-only instrument", r.InstrumentID)
	}
}
