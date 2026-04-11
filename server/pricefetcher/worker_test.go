package pricefetcher

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"github.com/leedenison/portfoliodb/server/telemetry"
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
		{Type: "MIC_TICKER", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "OCC", Value: "AAPL250321C00150000"},
	}
	got := pluginutil.FilterIdentifiers([]string{"MIC_TICKER", "OCC"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Type != "MIC_TICKER" || got[1].Type != "OCC" {
		t.Errorf("unexpected types: %s, %s", got[0].Type, got[1].Type)
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
		result:  &FetchResult{Bars: []DailyBar{{Date: from, Close: 1.08}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	// PriceGaps returns empty, FXGaps returns a gap for an FX instrument.
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: fxInstID, Ranges: []db.DateRange{{From: from, To: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{fxInstID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{fxInstID}).Return([]*db.InstrumentRow{
		{
			ID:         fxInstID,
			AssetClass: strPtr("FX"),
			Currency:   strPtr("USD"),
			Identifiers: []db.IdentifierInput{
				{Type: "FX_PAIR", Value: "EURUSD"},
			},
		},
	}, nil)
	mockDB.EXPECT().UpsertPricesWithFill(gomock.Any(), fxInstID, pluginID, gomock.Any(), from, to).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

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

func (s *filterStub) DisplayName() string                        { return "stub" }
func (s *filterStub) SupportedIdentifierTypes() []string         { return nil }
func (s *filterStub) AcceptableAssetClasses() map[string]bool    { return s.assetClasses }
func (s *filterStub) AcceptableExchanges() map[string]bool       { return s.exchanges }
func (s *filterStub) AcceptableCurrencies() map[string]bool      { return s.currencies }
func (s *filterStub) DefaultConfig() []byte                      { return nil }
func (s *filterStub) FetchPrices(_ context.Context, _ []byte, _ []Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
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

func (s *fetchStub) SupportedIdentifierTypes() []string { return s.idTypes }
func (s *fetchStub) FetchPrices(_ context.Context, _ []byte, _ []Identifier, _ string, _, _ time.Time) (*FetchResult, error) {
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
		result:  &FetchResult{Bars: []DailyBar{{Date: time.Now(), Close: 100}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, To: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	// Return blocked for this (instrument, plugin) pair.
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(
		map[string]map[string]bool{instID: {pluginID: true}}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
		},
	}, nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

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
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, To: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}")},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
		},
	}, nil)
	mockDB.EXPECT().CreatePriceFetchBlock(gomock.Any(), instID, pluginID, "ticker not found").Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

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
		result:  &FetchResult{Bars: []DailyBar{{Date: barDate, Close: 100}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, To: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}"), MaxHistoryDays: &maxDays},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
		},
	}, nil)
	mockDB.EXPECT().UpsertPricesWithFill(gomock.Any(), instID, pluginID, gomock.Any(), gomock.Any(), to).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

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
		result:  &FetchResult{Bars: []DailyBar{{Date: time.Now(), Close: 100}}},
	}
	reg := NewRegistry()
	reg.Register(pluginID, stub)

	now := time.Now().UTC().Truncate(db.Day)
	// Gap entirely before the max history limit.
	from := now.AddDate(0, 0, -90)
	to := now.AddDate(0, 0, -60)

	mockDB.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return([]db.InstrumentDateRanges{
		{InstrumentID: instID, Ranges: []db.DateRange{{From: from, To: to}}},
	}, nil)
	mockDB.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRow{
		{PluginID: pluginID, Precedence: 10, Config: []byte("{}"), MaxHistoryDays: &maxDays},
	}, nil)
	mockDB.EXPECT().BlockedPluginsForInstruments(gomock.Any(), []string{instID}).Return(nil, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{instID}).Return([]*db.InstrumentRow{
		{
			ID:         instID,
			AssetClass: strPtr("STOCK"),
			Identifiers: []db.IdentifierInput{
				{Type: "MIC_TICKER", Value: "AAPL"},
			},
		},
	}, nil)

	runCycle(ctx, mockDB, reg, nil, nil, nil)

	if stub.calls != 0 {
		t.Errorf("expected 0 FetchPrices calls for gap older than max history, got %d", stub.calls)
	}
}

// counterSpy counts Incr calls per key.
type counterSpy struct {
	telemetry.NoopCounter
	cycles atomic.Int64
}

func (c *counterSpy) Incr(_ context.Context, name string) {
	if name == "price_fetcher.cycles" {
		c.cycles.Add(1)
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

	counter := &counterSpy{}
	trigger := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunWorker(ctx, mockDB, NewRegistry(), counter, nil, trigger, nil)
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

	cycles := counter.cycles.Load()
	if cycles != 2 {
		t.Errorf("expected exactly 2 cycles (1 running + 1 buffered), got %d", cycles)
	}
}

