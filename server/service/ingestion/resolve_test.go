package ingestion

import (
	"context"
	"errors"
	"testing"

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

func (p *fakePlugin) Identify(ctx context.Context, broker, instrumentDescription string) (*identifier.Instrument, []identifier.Identifier, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return p.inst, p.ids, p.err
}

func TestResolve_DBHit_NoPluginCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "AAPL").
		Return("existing-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "AAPL", nil, 0)
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

func TestResolve_CacheHit_NoPluginCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	cache := make(map[string]resolveResult)
	key := cacheKey("IBKR", "GOOG")
	cache[key] = resolveResult{InstrumentID: "cached-id", FirstRowIndex: 0}

	// No DB or plugin calls when cache has entry
	r, err := Resolve(ctx, database, registry, "IBKR", "GOOG", cache, 1)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "cached-id" {
		t.Errorf("InstrumentID = %q, want cached-id", r.InstrumentID)
	}
}

func TestResolve_DBMiss_NoPlugins_BrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return(nil, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "UNKNOWN"}}).
		Return("broker-only-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "UNKNOWN", nil, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.InstrumentID != "broker-only-id" {
		t.Errorf("InstrumentID = %q, want broker-only-id", r.InstrumentID)
	}
	if r.IdErr == nil {
		t.Fatal("expected IdErr for broker description only")
	}
	if r.IdErr.Message != MsgBrokerDescriptionOnly {
		t.Errorf("IdErr.Message = %q, want %q", r.IdErr.Message, MsgBrokerDescriptionOnly)
	}
}

func TestResolve_DBMiss_AllPluginsErrNotIdentified_BrokerDescriptionOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	registry.Register("p1", &fakePlugin{err: identifier.ErrNotIdentified})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "UNKNOWN").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{{PluginID: "p1", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "UNKNOWN"}}).
		Return("broker-only-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "UNKNOWN", nil, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgBrokerDescriptionOnly {
		t.Errorf("expected IdErr message %q, got %+v", MsgBrokerDescriptionOnly, r.IdErr)
	}
}

func TestResolve_DBMiss_OnePluginSuccess_EnsureInstrumentWithResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	registry.Register("local", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "equity", Exchange: "XNAS", Currency: "USD", Name: "Apple Inc."},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "AAPL"}, {Type: "ISIN", Value: "US0378331005"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "AAPL").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{{PluginID: "local", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "equity", "XNAS", "USD", "Apple Inc.", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, idns []db.IdentifierInput) (string, error) {
			if len(idns) < 2 {
				t.Errorf("expected at least 2 identifiers (broker + ISIN), got %d", len(idns))
			}
			return "resolved-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", "AAPL", nil, 0)
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

func TestResolve_TwoPlugins_HigherPrecedenceWins(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "X"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "X"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "X").
		Return("", nil)
	// ListEnabledPluginConfigs returns precedence desc, so high (20) before low (10)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any()).
		Return("high-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "X", nil, 0)
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
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "Y"}, {Type: "CUSIP", Value: "12345"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "Y"}, {Type: "ISIN", Value: "US0000000000"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "Y").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, idns []db.IdentifierInput) (string, error) {
			// Merged: IBKR from high first, ISIN from high, CUSIP from low (different types).
			types := make(map[string]string)
			for _, idn := range idns {
				types[idn.Type] = idn.Value
			}
			if types["IBKR"] != "Y" || types["ISIN"] != "US0000000000" || types["CUSIP"] != "12345" {
				t.Errorf("merged identifiers: got %v, want IBKR=Y, ISIN=US0000000000, CUSIP=12345", types)
			}
			return "merged-id", nil
		})

	r, err := Resolve(ctx, database, registry, "IBKR", "Y", nil, 0)
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
	registry.Register("low", &fakePlugin{
		inst: &identifier.Instrument{Name: "Low"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "Z"}, {Type: "ISIN", Value: "LOW-ISIN"}},
		err:  nil,
	})
	registry.Register("high", &fakePlugin{
		inst: &identifier.Instrument{Name: "High"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "Z"}, {Type: "ISIN", Value: "HIGH-ISIN"}},
		err:  nil,
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "Z").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{
			{PluginID: "high", Precedence: 20, Config: nil},
			{PluginID: "low", Precedence: 10, Config: nil},
		}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "High", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _ string, idns []db.IdentifierInput) (string, error) {
			for _, idn := range idns {
				if idn.Type == "ISIN" && idn.Value != "HIGH-ISIN" {
					t.Errorf("same-type conflict: ISIN = %q, want HIGH-ISIN (high precedence)", idn.Value)
				}
			}
			return "id", nil
		})

	_, err := Resolve(ctx, database, registry, "IBKR", "Z", nil, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_PluginTimeout_FallbackAndMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	// Plugin that returns context.DeadlineExceeded (simulate timeout)
	registry.Register("slow", &fakePlugin{err: context.DeadlineExceeded})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "SLOW").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{{PluginID: "slow", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "SLOW"}}).
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "SLOW", nil, 0)
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
	registry.Register("bad", &fakePlugin{err: errors.New("connection refused")})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "BAD").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{{PluginID: "bad", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "BAD"}}).
		Return("fallback-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "BAD", nil, 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.IdErr == nil || r.IdErr.Message != MsgPluginUnavailable {
		t.Errorf("expected IdErr message %q, got %+v", MsgPluginUnavailable, r.IdErr)
	}
}

func TestTimeoutFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   []byte
		wantSecs float64 // approximate seconds we expect
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

func TestCacheKey(t *testing.T) {
	k := cacheKey("IBKR", "AAPL")
	if k != "IBKR\x00AAPL" {
		t.Errorf("cacheKey = %q, want IBKR\\x00AAPL", k)
	}
}

// retryPlugin fails once with a non-ErrNotIdentified error, then succeeds on retry.
type retryPlugin struct {
	callCount int
	inst      *identifier.Instrument
	ids       []identifier.Identifier
}

func (p *retryPlugin) Identify(ctx context.Context, broker, instrumentDescription string) (*identifier.Instrument, []identifier.Identifier, error) {
	p.callCount++
	if p.callCount == 1 {
		return nil, nil, errors.New("temporary failure")
	}
	return p.inst, p.ids, nil
}

func TestResolve_PluginFailsThenRetrySucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)
	registry := identifier.NewRegistry()
	registry.Register("retry", &retryPlugin{
		inst: &identifier.Instrument{Name: "Retried"},
		ids:  []identifier.Identifier{{Type: "IBKR", Value: "RETRY"}},
	})

	ctx := context.Background()
	database.EXPECT().
		FindInstrumentByBrokerDescription(gomock.Any(), "IBKR", "RETRY").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any()).
		Return([]db.PluginConfigRow{{PluginID: "retry", Precedence: 10, Config: nil}}, nil)
	database.EXPECT().
		EnsureInstrument(gomock.Any(), "", "", "", "Retried", gomock.Any()).
		Return("retried-id", nil)

	r, err := Resolve(ctx, database, registry, "IBKR", "RETRY", nil, 0)
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
