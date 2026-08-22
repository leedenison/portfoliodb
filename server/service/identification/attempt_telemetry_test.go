package identification

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

// telPlugin is a fakePlugin that also reports how the call went, which is the
// half of a plugin-call row only a plugin can supply.
type telPlugin struct {
	inst     *identifier.Instrument
	ids      []identifier.Identifier
	filtered []identifier.Identifier
	outcome  identifier.Outcome
	err      error
	// failFirst makes the first call fail so the retry loop runs once, which is
	// what puts a 1 in the retries column.
	failFirst bool
	calls     int
}

func (p *telPlugin) Identify(_ context.Context, _ []byte, _, _, _ string, _ identifier.Identity) (identifier.Result, error) {
	p.calls++
	if p.failFirst && p.calls == 1 {
		return identifier.Result{Telemetry: identifier.Telemetry{Outcome: identifier.OutcomeError}}, errTransport
	}
	return identifier.Result{
		Instrument:  p.inst,
		Identifiers: p.ids,
		Filtered:    p.filtered,
		Telemetry:   identifier.Telemetry{Outcome: p.outcome},
	}, p.err
}
func (p *telPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *telPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *telPlugin) DefaultConfig() []byte                      { return nil }
func (p *telPlugin) DisplayName() string                        { return "Tel" }

var errTransport = errTransportType{}

type errTransportType struct{}

func (errTransportType) Error() string { return "transport failed" }

// attemptSpy records the attempt rows a resolution writes, the plugin calls
// under each of them, and the identifiers each call claimed.
type attemptSpy struct {
	attempts []db.TelemetryIdentificationAttempt
	calls    map[string][]db.TelemetryIdentifierPluginCall
	// claims by plugin id, which is what the call id is derived from here.
	claims map[string][]db.TelemetryIdentifierClaim
}

func newAttemptSpy(t *testing.T, tel *mock.MockTelemetryDB) *attemptSpy {
	t.Helper()
	s := &attemptSpy{
		calls:  map[string][]db.TelemetryIdentifierPluginCall{},
		claims: map[string][]db.TelemetryIdentifierClaim{},
	}
	tel.EXPECT().WriteIdentificationAttempt(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, a db.TelemetryIdentificationAttempt) string {
			s.attempts = append(s.attempts, a)
			return string(rune('a' + len(s.attempts) - 1))
		}).AnyTimes()
	tel.EXPECT().WriteIdentifierPluginCall(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, c db.TelemetryIdentifierPluginCall) string {
			s.calls[c.PluginID] = append(s.calls[c.PluginID], c)
			return "call-" + c.PluginID
		}).AnyTimes()
	tel.EXPECT().WriteIdentifierClaim(gomock.Any(), gomock.Any()).
		Do(func(_ context.Context, c db.TelemetryIdentifierClaim) {
			plugin := strings.TrimPrefix(c.CallID, "call-")
			s.claims[plugin] = append(s.claims[plugin], c)
		}).AnyTimes()
	return s
}

// scope is the attempt a test resolves under.
func scope(tel *mock.MockTelemetryDB) Attempt {
	return Attempt{DB: tel, RunID: "run-1", KeyID: "key-1", Purpose: db.TelemetryPurposePrimary}
}

// TestAttemptDBShortCircuit pins the outcome that keeps a failure rate honest. A
// resolution answered from the instrument table called no plugin, so counting it
// among the attempts that reached one makes the rate fall as the table fills.
func TestAttemptDBShortCircuit(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("inst-1", "STOCK", "XNAS", "USD", nil)

	_, err := ResolveWithPlugins(context.Background(), database, identifier.NewRegistry(),
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	if len(spy.attempts) != 1 {
		t.Fatalf("wrote %d attempts, want 1", len(spy.attempts))
	}
	a := spy.attempts[0]
	if a.Outcome != db.TelemetryAttemptDBShortCircuit {
		t.Errorf("outcome = %q, want %q", a.Outcome, db.TelemetryAttemptDBShortCircuit)
	}
	if a.Purpose != db.TelemetryPurposePrimary || a.Depth != 0 || !a.HadIdentifierHints {
		t.Errorf("attempt = %+v, want a primary attempt at depth 0 with hints", a)
	}
	if a.AssetClass != "STOCK" {
		t.Errorf("asset class = %q, want the class it resolved to", a.AssetClass)
	}
	if len(spy.calls) != 0 {
		t.Errorf("wrote %d plugin calls for a resolution that called none", len(spy.calls))
	}
}

// TestAttemptNoEligiblePlugins pins the other denominator exclusion. A plugin the
// eligibility filter removed made no call, so it produces no row, and an attempt
// left with none records that rather than a failure to identify.
func TestAttemptNoEligiblePlugins(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	registry := identifier.NewRegistry()
	registry.Register("cash-only", &cashOnlyPlugin{})
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "cash-only", Precedence: 10}}, nil)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	if len(spy.attempts) != 1 || spy.attempts[0].Outcome != db.TelemetryAttemptNoEligiblePlugins {
		t.Fatalf("attempts = %+v, want one recording no eligible plugins", spy.attempts)
	}
	if len(spy.calls) != 0 {
		t.Errorf("wrote %d plugin calls for a plugin that was never called", len(spy.calls))
	}
}

// cashOnlyPlugin accepts nothing a stock hint routes to it.
type cashOnlyPlugin struct{ telPlugin }

func (p *cashOnlyPlugin) AcceptableSecurityTypes() map[string]bool {
	return map[string]bool{identifier.SecurityTypeHintCash: true}
}

// TestPluginCallOutcomesAreComposed pins the three outcomes no plugin can know.
// They are decided after every plugin has returned: the winner won, a plugin that
// agreed with it but did not win was superseded, and one contradicting it was
// discarded.
func TestPluginCallOutcomesAreComposed(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	// Highest precedence answers first but does not match the currency hint;
	// the middle one does, so it wins despite losing on precedence. The last
	// contradicts the winner's currency and is dropped from the merge.
	registry := identifier.NewRegistry()
	registry.Register("high", &telPlugin{
		inst:    &identifier.Instrument{Name: "Apple", AssetClass: "STOCK", Currency: "USD"},
		ids:     []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
		outcome: identifier.OutcomeIdentified,
	})
	registry.Register("mid", &telPlugin{
		inst:    &identifier.Instrument{Name: "Apple", AssetClass: "STOCK", Currency: "USD"},
		ids:     []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		outcome: identifier.OutcomeIdentified,
	})
	registry.Register("odd", &telPlugin{
		inst:    &identifier.Instrument{Name: "Apple", AssetClass: "STOCK", Currency: "EUR"},
		outcome: identifier.OutcomeIdentified,
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 30},
			{PluginID: "mid", Precedence: 20},
			{PluginID: "odd", Precedence: 10},
		}, nil)
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("inst-1", "listing-id", nil)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{Currency: "USD"}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	if len(spy.attempts) != 1 || spy.attempts[0].Outcome != db.TelemetryAttemptIdentified {
		t.Fatalf("attempts = %+v, want one identified", spy.attempts)
	}
	want := map[string]string{
		"high": db.TelemetryPluginCallWon,
		"mid":  db.TelemetryPluginCallSuperseded,
		"odd":  db.TelemetryPluginCallDiscardedInconsistent,
	}
	for id, w := range want {
		got := spy.calls[id]
		if len(got) != 1 {
			t.Errorf("wrote %d calls for %q, want 1", len(got), id)
			continue
		}
		if got[0].Outcome != w {
			t.Errorf("outcome for %q = %q, want %q", id, got[0].Outcome, w)
		}
		if got[0].AttemptID == "" || got[0].RunID != "run-1" {
			t.Errorf("call for %q = %+v, want it under the attempt and the run", id, got[0])
		}
	}
}

// TestPluginTransportOutcomePassesThrough pins the half of the vocabulary the
// plugin owns. Massive declining to call upstream about a long-expired contract
// is not an ordinary non-identification, and the row says so.
func TestPluginTransportOutcomePassesThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	registry := identifier.NewRegistry()
	registry.Register("expired", &telPlugin{
		outcome: identifier.OutcomeSkippedExpired,
		err:     identifier.ErrNotIdentified,
	})
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "expired", Precedence: 10}}, nil)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	if len(spy.attempts) != 1 || spy.attempts[0].Outcome != db.TelemetryAttemptNotIdentified {
		t.Fatalf("attempts = %+v, want one not identified", spy.attempts)
	}
	got := spy.calls["expired"]
	if len(got) != 1 || got[0].Outcome != string(identifier.OutcomeSkippedExpired) {
		t.Fatalf("calls = %+v, want one skipped_expired", got)
	}
	if got[0].Retries != 0 {
		t.Errorf("retries = %d, want 0: a non-identification is permanent", got[0].Retries)
	}
}

// TestPluginCallCountsRetries pins the retry column being the resolver's. The
// plugin never learns it was called twice; the loop that called it does.
func TestPluginCallCountsRetries(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	prev := PluginRetryBackoff
	PluginRetryBackoff = time.Millisecond
	t.Cleanup(func() { PluginRetryBackoff = prev })

	registry := identifier.NewRegistry()
	registry.Register("flaky", &telPlugin{
		failFirst: true,
		outcome:   identifier.OutcomeNotIdentified,
		err:       identifier.ErrNotIdentified,
	})
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "flaky", Precedence: 10}}, nil)

	if _, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{}},
		false, nil, scope(tel), nil, 0, nil); err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	got := spy.calls["flaky"]
	if len(got) != 1 {
		t.Fatalf("wrote %d calls, want 1 -- a row is one invocation, retries and all", len(got))
	}
	if got[0].Retries != 1 {
		t.Errorf("retries = %d, want 1", got[0].Retries)
	}
}

// TestUnderlyingRecursionIsItsOwnAttempt pins the purpose column. Resolving a
// derivative's underlying is a second ResolveWithPlugins call over the same key,
// and without purpose and depth it is indistinguishable from the first.
func TestUnderlyingRecursionIsItsOwnAttempt(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	registry := identifier.NewRegistry()
	registry.Register("opt", &telPlugin{
		inst: &identifier.Instrument{
			Name:                  "AAPL Call",
			AssetClass:            "OPTION",
			UnderlyingIdentifiers: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
		outcome: identifier.OutcomeIdentified,
	})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "OCC", "", "AAPL240315C00100000").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "OCC", "AAPL240315C00100000").Return("", nil)
	// The underlying resolves straight from the instrument table, which is a
	// second attempt that short-circuits.
	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("underlying-id", "STOCK", "XNAS", "USD", nil)
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "opt", Precedence: 10}}, nil)
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), "underlying-id", gomock.Any(), gomock.Any(), gomock.Any()).
		Return("opt-id", "listing-id", nil)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "OCC", Value: "AAPL240315C00100000"}}, Hints: identifier.Hints{}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	if len(spy.attempts) != 2 {
		t.Fatalf("wrote %d attempts, want 2 -- the option and its underlying", len(spy.attempts))
	}
	// The underlying finishes first, so it is written first.
	under, primary := spy.attempts[0], spy.attempts[1]
	if under.Purpose != db.TelemetryPurposeUnderlying || under.Depth != 1 {
		t.Errorf("underlying attempt = %+v, want purpose underlying at depth 1", under)
	}
	if under.Outcome != db.TelemetryAttemptDBShortCircuit {
		t.Errorf("underlying outcome = %q, want %q", under.Outcome, db.TelemetryAttemptDBShortCircuit)
	}
	if primary.Purpose != db.TelemetryPurposePrimary || primary.Depth != 0 {
		t.Errorf("primary attempt = %+v, want purpose primary at depth 0", primary)
	}
	if under.ResolutionKeyID != primary.ResolutionKeyID {
		t.Error("the underlying attempt hangs off a different key from the resolution that caused it")
	}
}

// TestIdentifierClaimsAreRecordedPerCall pins what one call said in one answer.
// The rows under a call id are the claim; the same identifiers spread over two
// calls are a set the resolver assembled, and telling those apart is the whole
// reason this is recorded per call rather than per attempt.
//
// A result discarded as inconsistent still made a claim. It contributes nothing
// to the instrument, and dropping its claim as well would leave the
// contradiction with nowhere to be read from afterwards.
func TestIdentifierClaimsAreRecordedPerCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tel := mock.NewMockTelemetryDB(ctrl)
	spy := newAttemptSpy(t, tel)

	registry := identifier.NewRegistry()
	// Returns a FIGI while strictly filtered on an ISIN it does not echo back.
	registry.Register("high", &telPlugin{
		inst:     &identifier.Instrument{Name: "Apple", AssetClass: "STOCK", Currency: "USD"},
		ids:      []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
		filtered: []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
		outcome:  identifier.OutcomeIdentified,
	})
	// Contradicts the winner's currency, so it is discarded from the merge.
	registry.Register("odd", &telPlugin{
		inst:    &identifier.Instrument{Name: "Apple", AssetClass: "STOCK", Currency: "EUR"},
		ids:     []identifier.Identifier{{Type: "CUSIP", Value: "037833100"}},
		outcome: identifier.OutcomeIdentified,
	})
	// Answered and had nothing, so it asserted nothing.
	registry.Register("quiet", &telPlugin{outcome: identifier.OutcomeNotIdentified, err: identifier.ErrNotIdentified})

	database.EXPECT().FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").Return("", nil)
	database.EXPECT().FindInstrumentByTickerIgnoringSeparators(gomock.Any(), "AAPL").Return("", nil).AnyTimes()
	database.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 30},
			{PluginID: "odd", Precedence: 20},
			{PluginID: "quiet", Precedence: 10},
		}, nil)
	database.EXPECT().EnsureInstrument(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return("inst-1", "listing-id", nil)

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Identity{Stated: []identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}}, Hints: identifier.Hints{Currency: "USD"}},
		false, nil, scope(tel), nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	// The winner's claim: what it returned and what it was filtered on, graded
	// alike and under one call.
	got := spy.claims["high"]
	if len(got) != 2 {
		t.Fatalf("high claimed %+v, want the FIGI and the ISIN", got)
	}
	roles := map[string]string{}
	for _, c := range got {
		roles[c.Ref.Type] = c.Role
		if c.RunID != "run-1" || c.CallID != "call-high" {
			t.Errorf("claim = %+v, want it under the call and the run", c)
		}
	}
	if roles["OPENFIGI_SHARE_CLASS"] != db.ClaimRoleReturned || roles["ISIN"] != db.ClaimRoleFiltered {
		t.Errorf("roles = %v", roles)
	}

	if len(spy.claims["odd"]) != 1 || spy.claims["odd"][0].Ref.Type != "CUSIP" {
		t.Errorf("odd claimed %+v; a discarded result still said something", spy.claims["odd"])
	}
	if len(spy.claims["quiet"]) != 0 {
		t.Errorf("quiet claimed %+v; an empty answer asserts nothing", spy.claims["quiet"])
	}
}
