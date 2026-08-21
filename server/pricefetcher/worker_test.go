package pricefetcher

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/worker"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
)

func strPtr(s string) *string { return &s }

func TestPluginAccepts(t *testing.T) {
	tests := []struct {
		name   string
		plugin *filterStub
		inst   *db.InstrumentRow
		want   bool
	}{
		{
			name:   "all nil filters accept anything",
			plugin: &filterStub{},
			inst:   &db.InstrumentRow{AssetClass: strPtr("STOCK"), ExchangeMIC: strPtr("XNAS"), Currency: strPtr("USD")},
			want:   true,
		},
		{
			name:   "asset class mismatch",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true}},
			inst:   &db.InstrumentRow{AssetClass: strPtr("OPTION")},
			want:   false,
		},
		{
			name:   "asset class match",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true, "ETF": true}},
			inst:   &db.InstrumentRow{AssetClass: strPtr("ETF")},
			want:   true,
		},
		{
			name:   "null asset class passes",
			plugin: &filterStub{assetClasses: map[string]bool{"STOCK": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
		{
			name:   "currency mismatch",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{Currency: strPtr("EUR")},
			want:   false,
		},
		{
			name:   "currency match case insensitive",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{Currency: strPtr("usd")},
			want:   true,
		},
		{
			name:   "null currency passes",
			plugin: &filterStub{currencies: map[string]bool{"USD": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
		{
			name:   "exchange mismatch",
			plugin: &filterStub{exchanges: map[string]bool{"XNAS": true}},
			inst:   &db.InstrumentRow{ExchangeMIC: strPtr("XNYS")},
			want:   false,
		},
		{
			name:   "null exchange passes",
			plugin: &filterStub{exchanges: map[string]bool{"XNAS": true}},
			inst:   &db.InstrumentRow{},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pluginutil.PluginAccepts(tc.plugin.AcceptableAssetClasses(), tc.plugin.AcceptableExchanges(), tc.plugin.AcceptableCurrencies(), tc.inst)
			if got != tc.want {
				t.Errorf("pluginAccepts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterIdentifiers(t *testing.T) {
	ids := []db.IdentifierInput{
		{
			Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
		},
		{
			Ref: db.InstrumentRef{Type: "ISIN", Value: "US0378331005"},
		},
		{
			Ref: db.InstrumentRef{Type: "OCC", Value: "AAPL250321C00150000"},
		}}
	got := pluginutil.FilterIdentifiers([]string{"MIC_TICKER", "OCC"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Ref.Type != "MIC_TICKER" || got[1].Ref.Type != "OCC" {
		t.Errorf("unexpected types: %s, %s", got[0].Ref.Type, got[1].Ref.Type)
	}
}

func TestTrigger(t *testing.T) {
	t.Run("nil channel", func(t *testing.T) {
		pluginutil.Trigger(nil) // should not panic
	})
	t.Run("sends signal", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		pluginutil.Trigger(ch)
		select {
		case <-ch:
		default:
			t.Error("expected signal")
		}
	})
	t.Run("non-blocking when full", func(t *testing.T) {
		ch := make(chan struct{}, 1)
		ch <- struct{}{}
		pluginutil.Trigger(ch) // should not block
	})
}

func TestRunCycle_FXGapsProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	fxInstID := "fx-eurusd"
	pluginID := "test-plugin"

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	stub := &fetchStub{
		idTypes: []string{"FX_PAIR"},
		result:  &FetchResult{Bars: []DailyBar{{Date: from, Close: decimal.RequireFromString("1.08")}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	// PriceGaps returns empty, FXGaps returns a gap for an FX instrument.
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: fxInstID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{fxInstID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{fxInstID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{fxInstID}).Return([]*db.InstrumentRow{
		{
			ID:         fxInstID,
			AssetClass: strPtr("FX"),
			Currency:   strPtr("USD"),
			Identifiers: []db.IdentifierInput{
				{
					Ref: db.InstrumentRef{Type: "FX_PAIR", Value: "EURUSD"},
				}},
		},
	}, nil)
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), fxInstID, pluginID, gomock.Any(), from, to, gomock.Any()).Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 1 {
		t.Errorf("expected 1 FetchPrices call for FX gap, got %d", stub.calls)
	}
}

// filterStub implements Plugin for testing pluginAccepts.
type filterStub struct {
	assetClasses map[string]bool
	exchanges    map[string]bool
	currencies   map[string]bool
}

func (s *filterStub) DisplayName() string                     { return "stub" }
func (s *filterStub) SupportedIdentifierTypes() []string      { return nil }
func (s *filterStub) AcceptableAssetClasses() map[string]bool { return s.assetClasses }
func (s *filterStub) AcceptableExchanges() map[string]bool    { return s.exchanges }
func (s *filterStub) AcceptableCurrencies() map[string]bool   { return s.currencies }
func (s *filterStub) DefaultConfig() []byte                   { return nil }
func (s *filterStub) FetchPrices(_ context.Context, _ []byte, _ []identifier.Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
	return nil, ErrNoData
}

// fetchStub is a test plugin that records calls and returns configured results.
type fetchStub struct {
	filterStub
	idTypes []string
	calls   int
	result  *FetchResult
	err     error
}

func d(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *fetchStub) SupportedIdentifierTypes() []string { return s.idTypes }
func (s *fetchStub) FetchPrices(_ context.Context, _ []byte, _ []identifier.Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
	s.calls++
	return s.result, s.err
}

func TestRunCycle_BlockedPluginSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"

	stub := &fetchStub{
		idTypes: []string{"MIC_TICKER"},
		result:  &FetchResult{Bars: []DailyBar{{Date: time.Now(), Close: decimal.RequireFromString("100")}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	// Return blocked for this (instrument, plugin) pair.
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(
		map[string]map[string]bool{instID: {pluginID: true}}, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{
					Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
				}},
		},
	}, nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 0 {
		t.Errorf("expected 0 FetchPrices calls for blocked plugin, got %d", stub.calls)
	}
}

func TestRunCycle_ErrPermanentCreatesBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"

	stub := &fetchStub{
		idTypes: []string{"MIC_TICKER"},
		err:     &ErrPermanent{Reason: "ticker not found"},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{
					Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
				}},
		},
	}, nil)
	mockDB.EXPECT().CreatePriceFetchBlock(gomock.Any(), instID, pluginID, "ticker not found").Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 1 {
		t.Errorf("expected 1 FetchPrices call, got %d", stub.calls)
	}
}

func TestRunCycle_MaxHistoryTruncation(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"
	maxDays := 30

	now := time.Now().UTC().Truncate(db.Day)
	// Gap that starts well before the max history limit.
	from := now.AddDate(0, 0, -60)
	to := now

	// Bar date must be within the truncated gap range [now-30, now).
	barDate := now.AddDate(0, 0, -1)
	stub := &fetchStub{
		idTypes: []string{"MIC_TICKER"},
		result:  &FetchResult{Bars: []DailyBar{{Date: barDate, Close: decimal.RequireFromString("100")}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}"), MaxHistoryDays: &maxDays},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{
					Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
				}},
		},
	}, nil)
	cutoff := now.AddDate(0, 0, -maxDays)
	// The head the plugin cannot reach is settled as covered by it, so the same
	// unreachable range is not rediscovered as a gap on the next cycle.
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), instID, pluginID, nil, from, cutoff, gomock.Any()).Return(nil)
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), instID, pluginID, gomock.Any(), cutoff, to, gomock.Any()).Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 1 {
		t.Errorf("expected 1 FetchPrices call (truncated), got %d", stub.calls)
	}
}

func TestRunCycle_MaxHistorySkipsOldGap(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"
	maxDays := 30

	stub := &fetchStub{
		idTypes: []string{"MIC_TICKER"},
		result:  &FetchResult{Bars: []DailyBar{{Date: time.Now(), Close: decimal.RequireFromString("100")}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	now := time.Now().UTC().Truncate(db.Day)
	// Gap entirely before the max history limit.
	from := now.AddDate(0, 0, -90)
	to := now.AddDate(0, 0, -60)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}"), MaxHistoryDays: &maxDays},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{
					Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
				}},
		},
	}, nil)
	// Wholly out of reach for this plugin, so it is recorded as covered by it
	// rather than being asked about again every cycle.
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), instID, pluginID, nil, from, to, gomock.Any()).Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 0 {
		t.Errorf("expected 0 FetchPrices calls for gap older than max history, got %d", stub.calls)
	}
}

// ErrNoData is an answer, not a failure to answer. Recording it as coverage is
// what stops a pre-IPO, delisted or untraded range being asked about forever.
func TestRunCycle_NoDataRecordsCoverage(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"
	from, to := d(2024, 1, 1), d(2024, 1, 11)

	stub := &fetchStub{idTypes: []string{"MIC_TICKER"}, err: ErrNoData}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{ID: instID, AssetClass: strPtr("STOCK"), Identifiers: []db.IdentifierInput{{
			Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
		}}},
	}, nil)
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), instID, pluginID, nil, from, to, gomock.Any()).Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 1 {
		t.Errorf("expected 1 FetchPrices call, got %d", stub.calls)
	}
}

// A range this plugin has already answered for is not put to it again, which is
// what makes the previous test's recording worth anything.
func TestRunCycle_CoveredRangeNotRefetched(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	pluginID := "test-plugin"
	from, to := d(2024, 1, 1), d(2024, 1, 11)

	stub := &fetchStub{idTypes: []string{"MIC_TICKER"}, err: ErrNoData}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(
		map[string]map[string][]db.DateRange{instID: {pluginID: {{From: from, Before: to}}}}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{ID: instID, AssetClass: strPtr("STOCK"), Identifiers: []db.IdentifierInput{{
			Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
		}}},
	}, nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if stub.calls != 0 {
		t.Errorf("expected no FetchPrices call for an already covered range, got %d", stub.calls)
	}
}

// Coverage is per plugin, so what one plugin has settled does not silence
// another -- including a plugin configured after the first gave up.
func TestRunCycle_OtherPluginStillAskedAfterCoverage(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	instID := "inst-1"
	coveredID, freshID := "covered-plugin", "fresh-plugin"
	from, to := d(2024, 1, 1), d(2024, 1, 11)

	covered := &fetchStub{idTypes: []string{"MIC_TICKER"}, err: ErrNoData}
	fresh := &fetchStub{idTypes: []string{"MIC_TICKER"},
		result: &FetchResult{Bars: []DailyBar{{Date: d(2024, 1, 3), Close: decimal.RequireFromString("100")}}}}
	reg := NewRegistry()
	reg.Register(coveredID, covered)
	reg.Register(freshID, fresh)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, Before: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: coveredID, Precedence: 20, Config: []byte("{}")},
		{PluginID: freshID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().PriceCoverageByPlugin(gomock.Any(), []string{instID}).Return(
		map[string]map[string][]db.DateRange{instID: {coveredID: {{From: from, Before: to}}}}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{ID: instID, AssetClass: strPtr("STOCK"), Identifiers: []db.IdentifierInput{{
			Ref: db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL"},
		}}},
	}, nil)
	mockDB.EXPECT().UpsertPricesForRange(gomock.Any(), instID, freshID, gomock.Any(), from, to, gomock.Any()).Return(nil)

	_ = runCycle(ctx, mockDB, reg, nil, nil, db.NopTelemetry{}, "")

	if covered.calls != 0 {
		t.Errorf("covered plugin should not be asked again, got %d calls", covered.calls)
	}
	if fresh.calls != 1 {
		t.Errorf("expected the uncovered plugin to be asked once, got %d", fresh.calls)
	}
}

func TestRunWorker_DebounceCollapsesTriggers(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)

	// PriceGaps blocks until gate is closed, giving us control over cycle duration.
	gate := make(chan struct{})
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
			<-gate
			return nil, nil
		},
	).Times(2) // expect exactly 2 cycles
	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)

	workers := worker.NewRegistry()
	trigger := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunWorker(ctx, mockDB, NewRegistry(), nil, nil, trigger, workers)
		close(done)
	}()

	// Send first trigger to start cycle 1.
	trigger <- struct{}{}

	// Wait briefly for the goroutine to enter PriceGaps (blocked on gate).
	time.Sleep(20 * time.Millisecond)

	// Send 2 more triggers while cycle 1 is in-flight.
	// Buffer holds 1, so one is queued and one is dropped.
	pluginutil.Trigger(trigger)
	pluginutil.Trigger(trigger)

	// Release both cycles.
	close(gate)

	// Wait for worker to go idle after processing both cycles.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	cycles := workerCycles(workers, "price_fetcher")
	if cycles != 2 {
		t.Errorf("expected exactly 2 cycles (1 running + 1 buffered), got %d", cycles)
	}
}

// workerCycles reads the completed-cycle count the registry holds for a worker.
func workerCycles(reg *worker.Registry, name string) int64 {
	for _, st := range reg.List() {
		if st.Name == name {
			return st.Cycles
		}
	}
	return 0
}
