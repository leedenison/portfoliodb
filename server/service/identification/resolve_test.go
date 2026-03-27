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
func (p *fakePlugin) AcceptableSecurityTypes() map[string]bool { return nil }
func (p *fakePlugin) DefaultConfig() []byte                    { return nil }
func (p *fakePlugin) DisplayName() string                      { return "Fake" }

func TestResolveByHintsDBOnly_ExactMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "US", "AAPL").
		Return("inst-1", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "TICKER", Domain: "US", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0] != "inst-1" {
		t.Errorf("got %v, want [inst-1]", ids)
	}
}

func TestResolveByHintsDBOnly_FallbackByTypeAndValue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	// Exact match fails (domain is empty, stored domain is "US")
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("", nil)
	// Fallback by (type, value) finds it
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "AAPL").
		Return("inst-1", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "TICKER", Domain: "", Value: "AAPL"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 || ids[0] != "inst-1" {
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
		{Type: "TICKER", Value: ""},
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
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "US", "AAPL").
		Return("inst-1", nil)
	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "ISIN", "", "US0378331005").
		Return("inst-1", nil)

	ids, err := ResolveByHintsDBOnly(context.Background(), database, []identifier.Identifier{
		{Type: "TICKER", Domain: "US", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
	})
	if err != nil {
		t.Fatalf("ResolveByHintsDBOnly: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("got %d ids, want 1 (deduplicated)", len(ids))
	}
}

func TestFilterIdentifierHints_ValidAndInvalid(t *testing.T) {
	hints := []identifier.Identifier{
		{Type: "TICKER", Value: "AAPL"},
		{Type: "BOGUS_TYPE", Value: "XYZ"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "", Value: "empty"},
	}
	out := FilterIdentifierHints(context.Background(), hints, nil)
	if len(out) != 2 {
		t.Fatalf("got %d hints, want 2", len(out))
	}
	if out[0].Type != "TICKER" || out[1].Type != "ISIN" {
		t.Errorf("got types %q, %q, want TICKER, ISIN", out[0].Type, out[1].Type)
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
	registry := identifier.NewRegistry()

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "AAPL").
		Return("existing-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}, {Type: "TICKER", Domain: "US", Value: "AAPL"}},
	})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "AAPL").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "XNAS", "USD", "Apple Inc.", "", "", gomock.Any(), "", nil, nil).
		Return("new-id", nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{SecurityTypeHint: identifier.SecurityTypeHintStock},
		[]identifier.Identifier{{Type: "TICKER", Value: "AAPL"}},
		false, nil, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "XYZ").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "XYZ").
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
		[]identifier.Identifier{{Type: "TICKER", Value: "XYZ"}},
		false, fallback, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "SLOW").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "SLOW").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "SLOW"}},
		false, fallback, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{err: identifier.ErrNotIdentified})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "XYZ").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "XYZ").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "XYZ"}},
		false, nil, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	source := "IBKR:test:statement"
	desc := "APPLE INC COM"
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple"},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", desc).
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", desc).
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "STOCK", "", "", "Apple", "", "", gomock.Any(), "", nil, nil).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ string, _, _ *time.Time) (string, error) {
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
		[]identifier.Identifier{{Type: "TICKER", Value: desc}},
		true, nil, nil, nil, 0)
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
	registry := identifier.NewRegistry()
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	database.EXPECT().
		FindInstrumentByIdentifier(gomock.Any(), "TICKER", "", "BAD").
		Return("", nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "TICKER", "BAD").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10}}, nil)

	fallback := func(_ context.Context, db db.DB) (string, error) {
		return "fallback-id", nil
	}

	result, err := ResolveWithPlugins(context.Background(), database, registry,
		"", "", "", identifier.Hints{},
		[]identifier.Identifier{{Type: "TICKER", Value: "BAD"}},
		false, fallback, nil, nil, 0)
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
		{Type: "TICKER", Domain: "US", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
	}
	got := HintsSummary(hints)
	want := "TICKER(US):AAPL, ISIN:US0378331005"
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
		ids:  []identifier.Identifier{{Type: "TICKER", Value: "X"}},
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

func (p *retryPlugin) AcceptableSecurityTypes() map[string]bool { return nil }
func (p *retryPlugin) DefaultConfig() []byte                    { return nil }
func (p *retryPlugin) DisplayName() string                      { return "Retry" }

func TestCallPluginWithRetry_RetrySucceeds(t *testing.T) {
	p := &retryPlugin{
		inst: &identifier.Instrument{Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "TICKER", Value: "X"}},
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

func (p *cancelOnRetryPlugin) AcceptableSecurityTypes() map[string]bool { return nil }
func (p *cancelOnRetryPlugin) DefaultConfig() []byte                    { return nil }
func (p *cancelOnRetryPlugin) DisplayName() string                      { return "CancelOnRetry" }
