package corporateevents

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func strPtr(s string) *string { return &s }

func TestComputeMissingIntervals_NoCoverage(t *testing.T) {
	gaps := computeMissingIntervals(d(2024, 1, 1), d(2024, 1, 31), nil)
	if len(gaps) != 1 {
		t.Fatalf("expected 1 gap, got %d", len(gaps))
	}
	if !gaps[0].From.Equal(d(2024, 1, 1)) || !gaps[0].To.Equal(d(2024, 1, 31)) {
		t.Errorf("unexpected gap: %+v", gaps[0])
	}
}

func TestComputeMissingIntervals_FullyCovered(t *testing.T) {
	cov := []db.CorporateEventCoverage{
		{CoveredFrom: d(2024, 1, 1), CoveredTo: d(2024, 1, 31)},
	}
	gaps := computeMissingIntervals(d(2024, 1, 1), d(2024, 1, 31), cov)
	if len(gaps) != 0 {
		t.Fatalf("expected 0 gaps, got %+v", gaps)
	}
}

func TestComputeMissingIntervals_PartialCoverage(t *testing.T) {
	// Required: Jan 1 .. Jan 31. Covered: Jan 5 .. Jan 10. Expect two gaps:
	// [Jan 1, Jan 4] and [Jan 11, Jan 31].
	cov := []db.CorporateEventCoverage{
		{CoveredFrom: d(2024, 1, 5), CoveredTo: d(2024, 1, 10)},
	}
	gaps := computeMissingIntervals(d(2024, 1, 1), d(2024, 1, 31), cov)
	if len(gaps) != 2 {
		t.Fatalf("expected 2 gaps, got %+v", gaps)
	}
	if !gaps[0].From.Equal(d(2024, 1, 1)) || !gaps[0].To.Equal(d(2024, 1, 4)) {
		t.Errorf("first gap: %+v", gaps[0])
	}
	if !gaps[1].From.Equal(d(2024, 1, 11)) || !gaps[1].To.Equal(d(2024, 1, 31)) {
		t.Errorf("second gap: %+v", gaps[1])
	}
}

func TestPluginAccepts(t *testing.T) {
	tests := []struct {
		name string
		p    *stubPlugin
		inst *db.InstrumentRow
		want bool
	}{
		{
			name: "all nil filters accept anything",
			p:    &stubPlugin{},
			inst: &db.InstrumentRow{AssetClass: strPtr("STOCK"), ExchangeMIC: strPtr("XNAS"), Currency: strPtr("USD")},
			want: true,
		},
		{
			name: "STOCK accepted, OPTION rejected",
			p:    &stubPlugin{assetClasses: map[string]bool{"STOCK": true, "ETF": true}},
			inst: &db.InstrumentRow{AssetClass: strPtr("OPTION")},
			want: false,
		},
		{
			name: "ETF accepted",
			p:    &stubPlugin{assetClasses: map[string]bool{"STOCK": true, "ETF": true}},
			inst: &db.InstrumentRow{AssetClass: strPtr("ETF")},
			want: true,
		},
		{
			name: "null asset class passes",
			p:    &stubPlugin{assetClasses: map[string]bool{"STOCK": true}},
			inst: &db.InstrumentRow{},
			want: true,
		},
		{
			name: "currency case insensitive match",
			p:    &stubPlugin{currencies: map[string]bool{"USD": true}},
			inst: &db.InstrumentRow{Currency: strPtr("usd")},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pluginAccepts(tc.p, tc.inst); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFilterIdentifiers(t *testing.T) {
	ids := []db.IdentifierInput{
		{Type: "MIC_TICKER", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "OPENFIGI_TICKER", Value: "BBG000B9XRY4"},
	}
	got := filterIdentifiers([]string{"MIC_TICKER", "OPENFIGI_TICKER"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestTimeoutFromConfig(t *testing.T) {
	if timeoutFromConfig(nil) != DefaultPluginTimeout {
		t.Error("nil config")
	}
	if timeoutFromConfig([]byte(`{"timeout_seconds": 30}`)) != 30*time.Second {
		t.Error("explicit 30s")
	}
	if timeoutFromConfig([]byte(`{"timeout_seconds": -5}`)) != DefaultPluginTimeout {
		t.Error("negative")
	}
	if timeoutFromConfig([]byte(`not json`)) != DefaultPluginTimeout {
		t.Error("invalid json")
	}
}

func TestTrigger(t *testing.T) {
	t.Run("nil channel", func(t *testing.T) { Trigger(nil) })
	t.Run("sends signal", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		Trigger(ch)
		select {
		case <-ch:
		default:
			t.Error("expected signal")
		}
	})
	t.Run("non-blocking when full", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		ch <- struct{}{}
		Trigger(ch)
	})
}

// stubPlugin is the minimal Plugin used by filter and registry tests.
type stubPlugin struct {
	name         string
	idTypes      []string
	assetClasses map[string]bool
	exchanges    map[string]bool
	currencies   map[string]bool
	calls        int
	result       *Events
	err          error
}

func (s *stubPlugin) DisplayName() string                     { return s.name }
func (s *stubPlugin) SupportedIdentifierTypes() []string      { return s.idTypes }
func (s *stubPlugin) AcceptableAssetClasses() map[string]bool { return s.assetClasses }
func (s *stubPlugin) AcceptableExchanges() map[string]bool    { return s.exchanges }
func (s *stubPlugin) AcceptableCurrencies() map[string]bool   { return s.currencies }
func (s *stubPlugin) DefaultConfig() []byte                   { return []byte(`{}`) }
func (s *stubPlugin) FetchEvents(_ context.Context, _ []byte, _ []Identifier, _ string, _, _ time.Time) (*Events, error) {
	s.calls++
	return s.result, s.err
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &stubPlugin{name: "Massive"}
	r.Register("massive", p)
	if r.Get("massive") != p {
		t.Fatal("expected registered plugin")
	}
	if r.Get("nope") != nil {
		t.Fatal("expected nil for unknown")
	}
	if r.GetDisplayName("massive") != "Massive" {
		t.Errorf("display name: %q", r.GetDisplayName("massive"))
	}
}

// TestRunCycle_EmptyResultRecordsCoverage verifies that a successful fetch
// returning zero events still records coverage and stops the precedence walk.
// This is the key behavioural difference vs the price worker.
func TestRunCycle_EmptyResultRecordsCoverage(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "11111111-1111-1111-1111-111111111111"
	earliest := d(2024, 1, 1)

	// Two plugins: high-precedence returns empty (success), low-precedence
	// would also return data but should not be called.
	highPrec := &stubPlugin{
		name:         "high",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		result:       &Events{}, // empty success
	}
	lowPrec := &stubPlugin{
		name:         "low",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		result:       &Events{Splits: []Split{{ExDate: d(2024, 6, 1), SplitFrom: "1", SplitTo: "2"}}},
	}
	reg := NewRegistry()
	reg.Register("high", highPrec)
	reg.Register("low", lowPrec)

	mockDB.EXPECT().HeldStockEtfInstruments(gomock.Any()).Return([]db.HeldInstrument{
		{InstrumentID: instID, EarliestTxDate: earliest},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCorporateEvent).Return([]db.PluginConfigRow{
		{PluginID: "high", Precedence: 20, Config: []byte("{}")},
		{PluginID: "low", Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedCorporateEventPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
	}, nil)
	mockDB.EXPECT().ListCorporateEventCoverage(gomock.Any(), []string{instID}).Return(nil, nil)

	// Coverage MUST be recorded for the empty success.
	mockDB.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), instID, "high", gomock.Any(), gomock.Any()).Return(nil)

	// No upserts for splits/dividends (empty result).
	// No call into the low-precedence plugin.
	// No recompute (no splits landed).

	runCycle(ctx, mockDB, reg, nil, nil, nil)

	if highPrec.calls != 1 {
		t.Errorf("high plugin: expected 1 call, got %d", highPrec.calls)
	}
	if lowPrec.calls != 0 {
		t.Errorf("low plugin: expected 0 calls, got %d", lowPrec.calls)
	}
}

// TestRunCycle_SplitsLandTriggerRecompute verifies that a successful fetch
// returning splits triggers UpsertStockSplits, UpsertCorporateEventCoverage,
// and RecomputeSplitAdjustments for the instrument.
func TestRunCycle_SplitsLandTriggerRecompute(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "22222222-2222-2222-2222-222222222222"

	plugin := &stubPlugin{
		name:         "massive",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		result: &Events{
			Splits: []Split{{ExDate: d(2024, 6, 9), SplitFrom: "1", SplitTo: "7"}},
		},
	}
	reg := NewRegistry()
	reg.Register("massive", plugin)

	mockDB.EXPECT().HeldStockEtfInstruments(gomock.Any()).Return([]db.HeldInstrument{
		{InstrumentID: instID, EarliestTxDate: d(2014, 1, 1)},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCorporateEvent).Return([]db.PluginConfigRow{
		{PluginID: "massive", Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedCorporateEventPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
	}, nil)
	mockDB.EXPECT().ListCorporateEventCoverage(gomock.Any(), []string{instID}).Return(nil, nil)

	mockDB.EXPECT().UpsertStockSplits(gomock.Any(), gomock.Any()).Return(nil)
	mockDB.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), instID, "massive", gomock.Any(), gomock.Any()).Return(nil)
	mockDB.EXPECT().RecomputeSplitAdjustments(gomock.Any(), instID).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

	if plugin.calls != 1 {
		t.Errorf("expected 1 call, got %d", plugin.calls)
	}
}

// TestRunCycle_PermanentErrorCreatesBlock verifies that ErrPermanent results
// in a fetch block and the precedence walk continues.
func TestRunCycle_PermanentErrorCreatesBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "33333333-3333-3333-3333-333333333333"

	failing := &stubPlugin{
		name:         "broken",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		err:          &ErrPermanent{Reason: "404 not found"},
	}
	good := &stubPlugin{
		name:         "good",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		result:       &Events{},
	}
	reg := NewRegistry()
	reg.Register("broken", failing)
	reg.Register("good", good)

	mockDB.EXPECT().HeldStockEtfInstruments(gomock.Any()).Return([]db.HeldInstrument{
		{InstrumentID: instID, EarliestTxDate: d(2024, 1, 1)},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCorporateEvent).Return([]db.PluginConfigRow{
		{PluginID: "broken", Precedence: 20, Config: []byte("{}")},
		{PluginID: "good", Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedCorporateEventPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
	}, nil)
	mockDB.EXPECT().ListCorporateEventCoverage(gomock.Any(), []string{instID}).Return(nil, nil)

	mockDB.EXPECT().CreateCorporateEventFetchBlock(gomock.Any(), instID, "broken", "404 not found").Return(nil)
	mockDB.EXPECT().UpsertCorporateEventCoverage(gomock.Any(), instID, "good", gomock.Any(), gomock.Any()).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

	if failing.calls != 1 || good.calls != 1 {
		t.Errorf("expected both plugins called once, got broken=%d good=%d", failing.calls, good.calls)
	}
}

// TestRunCycle_BlockedPluginSkipped verifies that fetch blocks are honored.
func TestRunCycle_BlockedPluginSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "44444444-4444-4444-4444-444444444444"

	plugin := &stubPlugin{
		name:         "blocked",
		idTypes:      []string{"MIC_TICKER"},
		assetClasses: map[string]bool{"STOCK": true, "ETF": true},
		result:       &Events{},
	}
	reg := NewRegistry()
	reg.Register("blocked", plugin)

	mockDB.EXPECT().HeldStockEtfInstruments(gomock.Any()).Return([]db.HeldInstrument{
		{InstrumentID: instID, EarliestTxDate: d(2024, 1, 1)},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryCorporateEvent).Return([]db.PluginConfigRow{
		{PluginID: "blocked", Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedCorporateEventPluginsForInstruments(gomock.Any(), []string{instID}).Return(
		map[string]map[string]bool{instID: {"blocked": true}}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}},
		},
	}, nil)
	mockDB.EXPECT().ListCorporateEventCoverage(gomock.Any(), []string{instID}).Return(nil, nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

	if plugin.calls != 0 {
		t.Errorf("expected 0 calls, got %d", plugin.calls)
	}
}
