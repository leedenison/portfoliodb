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
	inst *identifier.Instrument
	ids  []identifier.Identifier
	err  error
}

func (p *fakePlugin) Identify(_ context.Context, _ []byte, _, _, _ string, _ identifier.Hints, _ []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	return p.inst, p.ids, p.err
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("existing-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0, nil)
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "OPENFIGI_TICKER", Domain: "US", Value: "AAPL"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", "", "", gomock.Any(), "", nil, nil, nil).
		Return("new-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0, nil)
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

func TestResolveWithPlugins_AllPluginsFail_Fallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	database.EXPECT().LookupOperatingMIC(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, mic string) (string, error) { return mic, nil }).AnyTimes()
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "XYZ").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "XYZ").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	fallbackCalled := false
	fallback := func(_ context.Context, db db.DB) (string, error) {
		fallbackCalled = true
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "XYZ"}},
		false, fallback, nil, nil, 0, nil)
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "SLOW").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "SLOW").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "SLOW"}},
		false, fallback, nil, nil, 0, nil)
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "XYZ").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "XYZ").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "XYZ"}},
		false, nil, nil, nil, 0, nil)
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
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Apple", "", "", gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			hasSource := false
			for _, idn := range idns {
				if idn.Type == "BROKER_DESCRIPTION" && idn.Domain == source && idn.Value == desc && !idn.Canonical {
					hasSource = true
				}
			}
			if !hasSource {
				t.Errorf("expected BROKER_DESCRIPTION identifier for (source=%q, desc=%q)", source, desc)
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"IBKR", source, desc, identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: desc}},
		true, nil, nil, nil, 0, nil)
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "BAD").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "BAD").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "BAD"}},
		false, fallback, nil, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	if !result.HadError {
		t.Error("expected HadError = true")
	}
}

func TestTimeoutFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   []byte
		wantSecs float64
	}{
		{"nil", nil, 30},
		{"empty", []byte("{}"), 30},
		{"valid", []byte(`{"timeout_seconds": 5}`), 5},
		{"zero_ignored", []byte(`{"timeout_seconds": 0}`), 30},
		{"negative_ignored", []byte(`{"timeout_seconds": -1}`), 30},
		{"invalid_json", []byte(`{`), 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := timeoutFromConfig(tt.config)
			got := d.Seconds()
			if got < tt.wantSecs-0.5 || got > tt.wantSecs+0.5 {
				t.Errorf("timeoutFromConfig() = %v (%.1fs), want ~%.1fs", d, got, tt.wantSecs)
			}
		})
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
	inst, ids, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Hints{}, nil, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Name != "OK" {
		t.Errorf("inst.Name = %q, want OK", inst.Name)
	}
	if len(ids) != 1 {
		t.Errorf("len(ids) = %d, want 1", len(ids))
	}
}

func TestCallPluginWithRetry_ErrNotIdentified_NoRetry(t *testing.T) {
	p := &fakePlugin{err: identifier.ErrNotIdentified}
	_, _, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Hints{}, nil, time.Second, time.Millisecond)
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

func (p *retryPlugin) Identify(_ context.Context, _ []byte, _, _, _ string, _ identifier.Hints, _ []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	p.callCount++
	if p.callCount == 1 {
		return nil, nil, errors.New("temporary failure")
	}
	return p.inst, p.ids, nil
}

func (p *retryPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *retryPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *retryPlugin) DefaultConfig() []byte                    { return nil }
func (p *retryPlugin) DisplayName() string                      { return "Retry" }

func TestCallPluginWithRetry_RetrySucceeds(t *testing.T) {
	p := &retryPlugin{
		inst: &identifier.Instrument{Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "MIC_TICKER", Value: "X"}},
	}
	inst, _, err := callPluginWithRetry(context.Background(), p, nil, "", "", "X", identifier.Hints{}, nil, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Name != "Retried" {
		t.Errorf("inst.Name = %q, want Retried", inst.Name)
	}
	if p.callCount != 2 {
		t.Errorf("callCount = %d, want 2", p.callCount)
	}
}

func TestCallPluginWithRetry_ParentCancelStopsRetry(t *testing.T) {
	// Verify that cancelling the parent context propagates to the retry attempt
	// (i.e. we no longer use context.Background() for retry).
	ctx, cancel := context.WithCancel(context.Background())
	p := &cancelOnRetryPlugin{cancel: cancel, inst: &identifier.Instrument{Name: "Never"}}
	inst, _, err := callPluginWithRetry(ctx, p, nil, "", "", "X", identifier.Hints{}, nil, time.Second, time.Millisecond)
	if err == nil {
		t.Fatalf("expected error from cancelled context, got inst=%v", inst)
	}
}

// cancelOnRetryPlugin fails the first call with a transient error (triggering retry),
// then cancels the parent context so the retry's context is also cancelled.
type cancelOnRetryPlugin struct {
	cancel    context.CancelFunc
	callCount int
	inst      *identifier.Instrument
}

func (p *cancelOnRetryPlugin) Identify(ctx context.Context, _ []byte, _, _, _ string, _ identifier.Hints, _ []identifier.Identifier) (*identifier.Instrument, []identifier.Identifier, error) {
	p.callCount++
	if p.callCount == 1 {
		p.cancel()
		return nil, nil, errors.New("transient")
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return p.inst, nil, nil
}

func (p *cancelOnRetryPlugin) AcceptableInstrumentKinds() map[string]bool { return nil }
func (p *cancelOnRetryPlugin) AcceptableSecurityTypes() map[string]bool   { return nil }
func (p *cancelOnRetryPlugin) DefaultConfig() []byte                    { return nil }
func (p *cancelOnRetryPlugin) DisplayName() string                      { return "CancelOnRetry" }

// --- consistentWith tests ---

func TestConsistentWith_AllMatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNAS", Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNAS", Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
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
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
		t.Error("expected inconsistent on currency mismatch")
	}
}

func TestConsistentWith_ExchangeMismatch(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNAS", Currency: "USD"},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNYS", Currency: "USD"},
	}
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
		t.Error("expected inconsistent on exchange mismatch")
	}
}

func TestConsistentWith_EmptyFieldsSkipped(t *testing.T) {
	w := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNAS", Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Exchange: "", Currency: ""},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	}
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
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
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
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
	if !consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): XNAS/USD with ISIN
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	// Plugin B (lower precedence): XNYS/EUR with FIGI -- inconsistent
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNYS", Currency: "EUR", Name: "Pomme"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG999999999"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			for _, idn := range idns {
				if idn.Type == "OPENFIGI_SHARE_CLASS" {
					t.Errorf("OPENFIGI_GLOBAL from inconsistent plugin should not be merged, got %q", idn.Value)
				}
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0, nil)
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
	inst := &identifier.Instrument{Exchange: "XNYS"}

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
	inst := &identifier.Instrument{Exchange: "XNAS"}

	diffs := CompareHints(context.Background(), identifier.Hints{}, idnHints, inst, nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %v", diffs)
	}
}

func TestCompareHints_ExchangeEmptyDomainSkipped(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "", Value: "AAPL"}}
	inst := &identifier.Instrument{Exchange: "XNAS"}

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
	inst := &identifier.Instrument{Currency: "EUR", AssetClass: "ETF", Exchange: "XNYS"}
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
	database.EXPECT().SaveProviderIdentifiers(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	registry := identifier.NewRegistry()

	// Plugin A (higher precedence): XNAS/USD with ISIN
	registry.Register("pluginA", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})
	// Plugin B (lower precedence): XNAS/USD with FIGI -- consistent
	registry.Register("pluginB", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG000B9XRY4"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "MIC_TICKER", "", "AAPL").
		Return("", "", "", "", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "MIC_TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{
			{PluginID: "pluginA", Precedence: 100},
			{PluginID: "pluginB", Precedence: 50},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple", "", "", gomock.Any(), "", nil, nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time, _ *db.OptionFields) (string, error) {
			hasFIGI := false
			for _, idn := range idns {
				if idn.Type == "OPENFIGI_SHARE_CLASS" && idn.Value == "BBG000B9XRY4" {
					hasFIGI = true
				}
			}
			if !hasFIGI {
				t.Error("expected OPENFIGI_GLOBAL from consistent plugin to be merged")
			}
			return "id", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "MIC_TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0, nil)
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
		inst: &identifier.Instrument{Exchange: "XNAS", Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	}
	o := &pluginResult{
		inst: &identifier.Instrument{Exchange: "XNGS", Currency: "USD"},
		ids:  []identifier.Identifier{{Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8"}},
	}
	// Without normalizer: different exchanges are inconsistent.
	if consistentWith(context.Background(), nil, "a", "b", w, o, nil) {
		t.Error("expected inconsistent without normalizer")
	}
	// With normalizer: XNGS and XNAS map to the same operating MIC.
	if !consistentWith(context.Background(), nil, "a", "b", w, o, testMICNormalizer()) {
		t.Error("expected consistent with normalizer (XNGS -> XNAS)")
	}
}

func TestCompareHints_SegmentMICNormalized(t *testing.T) {
	idnHints := []identifier.Identifier{{Type: "MIC_TICKER", Domain: "XNGS", Value: "AAPL"}}
	inst := &identifier.Instrument{Exchange: "XNAS"}

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
