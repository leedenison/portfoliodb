package pricefetcher

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
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
			got := pluginAccepts(tc.plugin, tc.inst)
			if got != tc.want {
				t.Errorf("pluginAccepts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilterIdentifiers(t *testing.T) {
	ids := []db.IdentifierInput{
		{Type: "TICKER", Value: "AAPL"},
		{Type: "ISIN", Value: "US0378331005"},
		{Type: "OCC", Value: "AAPL250321C00150000"},
	}
	got := filterIdentifiers([]string{"TICKER", "OCC"}, ids)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Type != "TICKER" || got[1].Type != "OCC" {
		t.Errorf("unexpected types: %s, %s", got[0].Type, got[1].Type)
	}
}

func TestTrigger(t *testing.T) {
	t.Run("nil channel", func(t *testing.T) {
		Trigger(nil) // should not panic
	})
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
		Trigger(ch) // should not block
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
	mockDB.EXPECT().GetInstrument(gomock.Any(), fxInstID).Return(&db.InstrumentRow{
		ID:         fxInstID,
		AssetClass: strPtr("FX"),
		Currency:   strPtr("USD"),
		Identifiers: []db.IdentifierInput{
			{Type: "FX_PAIR", Value: "EURUSD"},
		},
	}, nil)
	mockDB.EXPECT().LastRealPrice(gomock.Any(), fxInstID, from).Return(0.0, "", false, nil)
	mockDB.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil)

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
		idTypes: []string{"TICKER"},
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
	mockDB.EXPECT().GetInstrument(gomock.Any(), instID).Return(&db.InstrumentRow{
		ID:         instID,
		AssetClass: strPtr("STOCK"),
		Identifiers: []db.IdentifierInput{
			{Type: "TICKER", Value: "AAPL"},
		},
	}, nil)

	runCycle(ctx, mockDB, reg, nil, nil)

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
		idTypes: []string{"TICKER"},
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
	mockDB.EXPECT().GetInstrument(gomock.Any(), instID).Return(&db.InstrumentRow{
		ID:         instID,
		AssetClass: strPtr("STOCK"),
		Identifiers: []db.IdentifierInput{
			{Type: "TICKER", Value: "AAPL"},
		},
	}, nil)
	mockDB.EXPECT().CreatePriceFetchBlock(gomock.Any(), instID, pluginID, "ticker not found").Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil)

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
		idTypes: []string{"TICKER"},
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
	mockDB.EXPECT().GetInstrument(gomock.Any(), instID).Return(&db.InstrumentRow{
		ID:         instID,
		AssetClass: strPtr("STOCK"),
		Identifiers: []db.IdentifierInput{
			{Type: "TICKER", Value: "AAPL"},
		},
	}, nil)
	mockDB.EXPECT().LastRealPrice(gomock.Any(), instID, gomock.Any()).Return(0.0, "", false, nil)
	mockDB.EXPECT().UpsertPrices(gomock.Any(), gomock.Any()).Return(nil)

	runCycle(ctx, mockDB, reg, nil, nil)

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
		idTypes: []string{"TICKER"},
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
	mockDB.EXPECT().GetInstrument(gomock.Any(), instID).Return(&db.InstrumentRow{
		ID:         instID,
		AssetClass: strPtr("STOCK"),
		Identifiers: []db.IdentifierInput{
			{Type: "TICKER", Value: "AAPL"},
		},
	}, nil)

	runCycle(ctx, mockDB, reg, nil, nil)

	if stub.calls != 0 {
		t.Errorf("expected 0 FetchPrices calls for gap older than max history, got %d", stub.calls)
	}
}

// --- fillGaps tests ---

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func TestFillGaps_BasicWeekend(t *testing.T) {
	instID := "inst-1"
	provider := "test"
	// Mon-Fri bars, gap range is Mon-Mon (7 days).
	mon := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := []DailyBar{
		{Date: mon, Open: f64(100), High: f64(105), Low: f64(99), Close: 102, Volume: i64(1000)},
		{Date: mon.AddDate(0, 0, 1), Close: 103},
		{Date: mon.AddDate(0, 0, 2), Close: 104},
		{Date: mon.AddDate(0, 0, 3), Close: 105},
		{Date: mon.AddDate(0, 0, 4), Close: 106}, // Fri
	}
	from := mon
	to := mon.AddDate(0, 0, 7) // next Mon (exclusive)

	got := fillGaps(instID, provider, bars, from, to, 0, "", false)
	if len(got) != 7 {
		t.Fatalf("expected 7 prices, got %d", len(got))
	}
	// Mon-Fri should be real.
	for i := 0; i < 5; i++ {
		if got[i].Synthetic {
			t.Errorf("day %d: expected real, got synthetic", i)
		}
	}
	// Sat-Sun should be synthetic with Friday's close.
	for i := 5; i < 7; i++ {
		if !got[i].Synthetic {
			t.Errorf("day %d: expected synthetic, got real", i)
		}
		if got[i].Close != 106 {
			t.Errorf("day %d: close = %v, want 106", i, got[i].Close)
		}
		if got[i].Open != nil || got[i].High != nil || got[i].Low != nil || got[i].Volume != nil {
			t.Errorf("day %d: synthetic should have nil OHLV fields", i)
		}
	}
}

func TestFillGaps_NoBarsNoSeed(t *testing.T) {
	got := fillGaps("inst-1", "test", nil, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC), 0, "", false)
	if got != nil {
		t.Errorf("expected nil, got %d prices", len(got))
	}
}

func TestFillGaps_NoBarsWithSeed(t *testing.T) {
	from := time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC) // Sat
	to := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)   // Mon (exclusive)

	got := fillGaps("inst-1", "test", nil, from, to, 100.0, "seed-provider", true)
	if len(got) != 2 {
		t.Fatalf("expected 2 prices, got %d", len(got))
	}
	for _, p := range got {
		if !p.Synthetic {
			t.Error("expected synthetic")
		}
		if p.Close != 100.0 {
			t.Errorf("close = %v, want 100.0", p.Close)
		}
		if p.DataProvider != "seed-provider" {
			t.Errorf("provider = %q, want seed-provider", p.DataProvider)
		}
	}
}

func TestFillGaps_NoSeedAtStart(t *testing.T) {
	// Gap starts on day 1, first bar on day 3. Days 1-2 should be skipped.
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	bars := []DailyBar{
		{Date: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Close: 50.0},
	}

	got := fillGaps("inst-1", "test", bars, from, to, 0, "", false)
	// Day 1, 2 skipped (no seed). Day 3 real, day 4 synthetic.
	if len(got) != 2 {
		t.Fatalf("expected 2 prices, got %d", len(got))
	}
	if got[0].Synthetic || got[0].PriceDate != time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC) {
		t.Errorf("day 0: expected real on Jan 3")
	}
	if !got[1].Synthetic || got[1].Close != 50.0 {
		t.Errorf("day 1: expected synthetic with close=50.0")
	}
}

func TestFillGaps_SeedFromDB(t *testing.T) {
	// Seed from DB fills leading days before first bar.
	from := time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC) // Sat
	to := time.Date(2024, 1, 9, 0, 0, 0, 0, time.UTC)   // Tue (exclusive)
	bars := []DailyBar{
		{Date: time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC), Close: 110.0}, // Mon
	}

	got := fillGaps("inst-1", "test", bars, from, to, 100.0, "seed", true)
	if len(got) != 3 {
		t.Fatalf("expected 3 prices, got %d", len(got))
	}
	// Sat, Sun should be synthetic from seed (100.0).
	if !got[0].Synthetic || got[0].Close != 100.0 {
		t.Errorf("Sat: expected synthetic close=100.0, got synthetic=%v close=%v", got[0].Synthetic, got[0].Close)
	}
	if !got[1].Synthetic || got[1].Close != 100.0 {
		t.Errorf("Sun: expected synthetic close=100.0, got synthetic=%v close=%v", got[1].Synthetic, got[1].Close)
	}
	// Mon should be real.
	if got[2].Synthetic || got[2].Close != 110.0 {
		t.Errorf("Mon: expected real close=110.0, got synthetic=%v close=%v", got[2].Synthetic, got[2].Close)
	}
}
