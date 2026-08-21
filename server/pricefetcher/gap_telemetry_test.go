package pricefetcher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
)

// TestGapOutcome pins what a cycle concludes about one instrument. The two
// settled_empty rows are the point of the table: a plugin that covered every range
// and returned nothing has settled the gap, and so has a gap every eligible plugin
// had covered on an earlier cycle. Reading either as a failure would fill a panel
// meant to show outages with untraded weeks.
func TestGapOutcome(t *testing.T) {
	tests := []struct {
		name    string
		fetched bool
		bars    int
		reached bool
		called  bool
		want    string
	}{
		{"prices arrived", true, 40, true, true, db.TelemetryGapFilled},
		{"covered, nothing to give", true, 0, true, true, db.TelemetryGapSettledEmpty},
		{"already covered by every plugin", false, 0, true, false, db.TelemetryGapSettledEmpty},
		{"no plugin took it", false, 0, false, false, db.TelemetryGapNoEligiblePlugin},
		{"every plugin failed", false, 0, true, true, db.TelemetryGapAllPluginsFailed},
		// A plugin returned bars and a later range then failed, so the gap is not
		// covered. The bars that did arrive do not make it filled.
		{"partly fetched then failed", false, 12, true, true, db.TelemetryGapAllPluginsFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gapOutcome(tc.fetched, tc.bars, tc.reached, tc.called)
			if got != tc.want {
				t.Errorf("gapOutcome = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRangesDays sums the ask a gap represents. The ranges are half-open and
// UTC-truncated, so this is a subtraction and not calendar arithmetic.
func TestRangesDays(t *testing.T) {
	tests := []struct {
		name   string
		ranges []db.DateRange
		want   int
	}{
		{"none", nil, 0},
		{"one week", []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 8)}}, 7},
		{
			name: "two disjoint",
			ranges: []db.DateRange{
				{From: d(2024, 1, 1), Before: d(2024, 1, 8)},
				{From: d(2024, 3, 1), Before: d(2024, 3, 4)},
			},
			want: 10,
		},
		// A range whose end is not after its start asks for nothing and must not
		// subtract from the total.
		{"empty range", []db.DateRange{{From: d(2024, 1, 8), Before: d(2024, 1, 8)}}, 0},
		// A leap day is a day. The subtraction gets this right and a month-based
		// calculation would not.
		{"across a leap day", []db.DateRange{{From: d(2024, 2, 28), Before: d(2024, 3, 1)}}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rangesDays(tc.ranges); got != tc.want {
				t.Errorf("rangesDays = %d, want %d", got, tc.want)
			}
		})
	}
}

// telRecorder collects what a cycle wrote, so a test can assert on the rows rather
// than on the order the writer was called in.
type telRecorder struct {
	gaps     []db.TelemetryPriceGap
	outcomes map[string]string
	calls    map[string][]db.TelemetryPricePluginCall
}

// gapID is the id the recorder hands back for the nth gap written, counting from 1.
func gapID(n int) string { return fmt.Sprintf("gap-%d", n) }

func newTelRecorder(ctrl *gomock.Controller) (*mock.MockTelemetryDB, *telRecorder) {
	tel := mock.NewMockTelemetryDB(ctrl)
	r := &telRecorder{
		outcomes: make(map[string]string),
		calls:    make(map[string][]db.TelemetryPricePluginCall),
	}
	tel.EXPECT().StartPriceGap(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(_ context.Context, g db.TelemetryPriceGap) string {
			r.gaps = append(r.gaps, g)
			return gapID(len(r.gaps))
		})
	tel.EXPECT().EndPriceGap(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Do(func(_ context.Context, _, id, outcome string) { r.outcomes[id] = outcome })
	tel.EXPECT().WritePricePluginCall(gomock.Any(), gomock.Any()).AnyTimes().
		Do(func(_ context.Context, c db.TelemetryPricePluginCall) {
			r.calls[c.GapID] = append(r.calls[c.GapID], c)
		})
	return tel, r
}

// gapCycle wires the reads a cycle makes so a test states only what it is about.
// The instrument list is derived from the gaps, so a caller supplying an
// instrument row for every gap gets a cycle that reaches the plugins.
type gapCycle struct {
	priceGaps []db.InstrumentDateRanges
	fxGaps    []db.InstrumentDateRanges
	configs   []db.PluginConfigRow
	insts     []*db.InstrumentRow
	coverage  map[string]map[string][]db.DateRange
}

func (c gapCycle) expect(m *mock.MockDB) {
	m.EXPECT().PriceGaps(gomock.Any(), gomock.Any()).Return(c.priceGaps, nil)
	m.EXPECT().FXGaps(gomock.Any(), gomock.Any()).Return(c.fxGaps, nil)
	m.EXPECT().ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryPrice).
		Return(c.configs, nil).AnyTimes()
	m.EXPECT().BlockedPluginsForInstruments(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(c.insts, nil).AnyTimes()
	m.EXPECT().PriceCoverageByPlugin(gomock.Any(), gomock.Any()).Return(c.coverage, nil).AnyTimes()
	m.EXPECT().UpsertPricesForRange(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
		gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.EXPECT().CreatePriceFetchBlock(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil).AnyTimes()
}

// TestCycleRecordsWhatItWasAskedFor pins the gap row's inputs. The FX flag is what
// keeps a missing rate apart from a missing quote once the two lists are one, and
// the three attributes are the ones plugin filtering reads -- the MIC rather than
// the denormalised display exchange.
func TestCycleRecordsWhatItWasAskedFor(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 11)}}},
		},
		fxGaps: []db.InstrumentDateRanges{
			{InstrumentID: "fx-1", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 4)}}},
		},
		configs: []db.PluginConfigRow{{PluginID: "eodhd", Precedence: 10, Config: []byte("{}")}},
		insts: []*db.InstrumentRow{
			{ID: "inst-1", AssetClass: strPtr("STOCK"), Currency: strPtr("USD"),
				ExchangeMIC: strPtr("XNAS"), Exchange: "NASDAQ"},
			{ID: "fx-1", AssetClass: strPtr("FX"), Currency: strPtr("USD")},
		},
	}.expect(mockDB)

	// A registered plugin that supports an identifier type neither instrument
	// carries. Nothing is fetched, and unlike the no-plugin path the cycle still
	// loads the instruments, which is what puts the attributes on the rows.
	reg := NewRegistry()
	reg.Register("eodhd", &fetchStub{idTypes: []string{"SEDOL"}})

	if err := runCycle(context.Background(), mockDB, reg, nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	if len(rec.gaps) != 2 {
		t.Fatalf("recorded %d gaps, want 2", len(rec.gaps))
	}
	price, fx := rec.gaps[0], rec.gaps[1]
	if price.IsFX {
		t.Error("a PriceGaps entry was recorded as FX")
	}
	if !fx.IsFX {
		t.Error("an FXGaps entry was not recorded as FX")
	}
	if price.DaysOutstanding != 10 {
		t.Errorf("days_outstanding = %d, want 10", price.DaysOutstanding)
	}
	if fx.DaysOutstanding != 3 {
		t.Errorf("fx days_outstanding = %d, want 3", fx.DaysOutstanding)
	}
	if price.Exchange != "XNAS" {
		t.Errorf("exchange = %q, want the MIC the plugin filter compares", price.Exchange)
	}
	if price.AssetClass != "STOCK" || price.Currency != "USD" {
		t.Errorf("asset class / currency = %q / %q, want STOCK / USD",
			price.AssetClass, price.Currency)
	}
	// The plugin held no identifier it could use, so it never became a candidate
	// and wrote no row of its own.
	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapNoEligiblePlugin {
		t.Errorf("gap outcome = %q, want %q", got, db.TelemetryGapNoEligiblePlugin)
	}
	if n := len(rec.calls[gapID(1)]); n != 0 {
		t.Errorf("recorded %d calls for a plugin that was never asked, want 0", n)
	}
}

// TestCycleRecordsAFilledGap pins the ordinary success: one call carrying the bars
// it returned and the range it was asked for, and a gap stamped filled.
func TestCycleRecordsAFilledGap(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	from, to := d(2024, 1, 1), d(2024, 1, 3)
	stub := &fetchStub{
		idTypes: []string{"MIC_TICKER"},
		result: &FetchResult{Bars: []DailyBar{
			{Date: from, Close: decimal.RequireFromString("100")},
			{Date: d(2024, 1, 2), Close: decimal.RequireFromString("101")},
		}},
	}
	reg := NewRegistry()
	reg.Register("eodhd", stub)

	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: from, Before: to}}},
		},
		configs: []db.PluginConfigRow{{PluginID: "eodhd", Precedence: 70, Config: []byte("{}")}},
		insts: []*db.InstrumentRow{{
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:AAPL"}},
		}},
	}.expect(mockDB)

	if err := runCycle(context.Background(), mockDB, reg, nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapFilled {
		t.Errorf("gap outcome = %q, want %q", got, db.TelemetryGapFilled)
	}
	calls := rec.calls[gapID(1)]
	if len(calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Outcome != db.TelemetryPriceCallBarsReturned {
		t.Errorf("call outcome = %q, want %q", c.Outcome, db.TelemetryPriceCallBarsReturned)
	}
	if c.Bars != 2 {
		t.Errorf("bars = %d, want 2", c.Bars)
	}
	if !c.From.Equal(from) || !c.Before.Equal(to) {
		t.Errorf("range = [%s, %s), want [%s, %s)", c.From, c.Before, from, to)
	}
	// The configured precedence, not a loop index, so a plugin skipped by a filter
	// reads as a gap in the sequence.
	if c.Precedence != 70 {
		t.Errorf("precedence = %d, want the configured 70", c.Precedence)
	}
	if c.Duration == nil {
		t.Error("a completed call recorded no duration")
	}
}

// TestCycleRecordsAnEmptyAnswerAsSettled pins that a provider with nothing to say
// is not a failure. The range is covered so it is never asked about again, and the
// gap is settled rather than left looking like an outage.
func TestCycleRecordsAnEmptyAnswerAsSettled(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	stub := &fetchStub{idTypes: []string{"MIC_TICKER"}, err: ErrNoData}
	reg := NewRegistry()
	reg.Register("eodhd", stub)

	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 3)}}},
		},
		configs: []db.PluginConfigRow{{PluginID: "eodhd", Precedence: 70, Config: []byte("{}")}},
		insts: []*db.InstrumentRow{{
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:AAPL"}},
		}},
	}.expect(mockDB)

	if err := runCycle(context.Background(), mockDB, reg, nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapSettledEmpty {
		t.Errorf("gap outcome = %q, want %q", got, db.TelemetryGapSettledEmpty)
	}
	calls := rec.calls[gapID(1)]
	if len(calls) != 1 || calls[0].Outcome != db.TelemetryPriceCallNoData {
		t.Fatalf("calls = %+v, want one no_data", calls)
	}
	if calls[0].Bars != 0 {
		t.Errorf("bars = %d, want 0", calls[0].Bars)
	}
}

// TestCycleRecordsAHistoryLimitWithoutADuration pins the one outcome that made no
// call. A zero duration would average into the latency panel as a plugin that
// answered instantly, which is why the column is nullable.
func TestCycleRecordsAHistoryLimitWithoutADuration(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	stub := &fetchStub{idTypes: []string{"MIC_TICKER"}}
	reg := NewRegistry()
	reg.Register("eodhd", stub)

	maxHist := 30
	gapCycle{
		// Long before any 30 day reach, so the whole range is settled without a call.
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: d(2019, 1, 1), Before: d(2019, 2, 1)}}},
		},
		configs: []db.PluginConfigRow{
			{PluginID: "eodhd", Precedence: 70, Config: []byte("{}"), MaxHistoryDays: &maxHist},
		},
		insts: []*db.InstrumentRow{{
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:AAPL"}},
		}},
	}.expect(mockDB)

	if err := runCycle(context.Background(), mockDB, reg, nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	if stub.calls != 0 {
		t.Errorf("FetchPrices was called %d times for a range beyond the plugin's reach", stub.calls)
	}
	calls := rec.calls[gapID(1)]
	if len(calls) != 1 || calls[0].Outcome != db.TelemetryPriceCallHistoryLimit {
		t.Fatalf("calls = %+v, want one history_limit", calls)
	}
	if calls[0].Duration != nil {
		t.Errorf("duration = %v, want none for a call that never happened", *calls[0].Duration)
	}
	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapSettledEmpty {
		t.Errorf("gap outcome = %q, want %q", got, db.TelemetryGapSettledEmpty)
	}
}

// TestCycleRecordsAPluginFailure pins that a provider error stamps the gap as one
// nothing could fill, so it is visible as a recurring hole rather than as silence.
func TestCycleRecordsAPluginFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	stub := &fetchStub{idTypes: []string{"MIC_TICKER"}, err: errors.New("502 bad gateway")}
	reg := NewRegistry()
	reg.Register("eodhd", stub)

	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 3)}}},
		},
		configs: []db.PluginConfigRow{{PluginID: "eodhd", Precedence: 70, Config: []byte("{}")}},
		insts: []*db.InstrumentRow{{
			ID:          "inst-1",
			Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:AAPL"}},
		}},
	}.expect(mockDB)

	if err := runCycle(context.Background(), mockDB, reg, nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	calls := rec.calls[gapID(1)]
	if len(calls) != 1 || calls[0].Outcome != db.TelemetryPriceCallError {
		t.Fatalf("calls = %+v, want one error", calls)
	}
	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapAllPluginsFailed {
		t.Errorf("gap outcome = %q, want %q", got, db.TelemetryGapAllPluginsFailed)
	}
}

// TestCycleWithNoPluginRecordsEveryGap pins the misconfiguration that used to be
// invisible. A cycle with work and nowhere to send it returned silently and
// stamped its run success, which is indistinguishable from a cycle that found
// nothing to do.
func TestCycleWithNoPluginRecordsEveryGap(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 3)}}},
			{InstrumentID: "inst-2", Ranges: []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 3)}}},
		},
		configs: nil,
	}.expect(mockDB)

	if err := runCycle(context.Background(), mockDB, NewRegistry(), nil, nil, tel, "run-1"); err != nil {
		t.Fatalf("runCycle: %v", err)
	}

	if len(rec.gaps) != 2 {
		t.Fatalf("recorded %d gaps, want 2", len(rec.gaps))
	}
	for i := 1; i <= 2; i++ {
		if got := rec.outcomes[gapID(i)]; got != db.TelemetryGapNoEligiblePlugin {
			t.Errorf("gap %d outcome = %q, want %q", i, got, db.TelemetryGapNoEligiblePlugin)
		}
	}
}

// TestCancelledCycleStopsAndLeavesTheRestUnstamped pins how far a killed cycle
// got. The gaps it never reached keep a null outcome, which is what makes the
// stopping point readable, and the error is what stops the run being stamped
// success after covering one instrument of two.
func TestCancelledCycleStopsAndLeavesTheRestUnstamped(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockDB := mock.NewMockDB(ctrl)
	tel, rec := newTelRecorder(ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	stub := &cancelStub{
		fetchStub: fetchStub{idTypes: []string{"MIC_TICKER"},
			result: &FetchResult{Bars: []DailyBar{{Date: d(2024, 1, 1), Close: decimal.RequireFromString("100")}}}},
		cancel: cancel,
	}
	reg := NewRegistry()
	reg.Register("eodhd", stub)

	rng := []db.DateRange{{From: d(2024, 1, 1), Before: d(2024, 1, 3)}}
	gapCycle{
		priceGaps: []db.InstrumentDateRanges{
			{InstrumentID: "inst-1", Ranges: rng},
			{InstrumentID: "inst-2", Ranges: rng},
		},
		configs: []db.PluginConfigRow{{PluginID: "eodhd", Precedence: 70, Config: []byte("{}")}},
		insts: []*db.InstrumentRow{
			{ID: "inst-1", Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:AAPL"}}},
			{ID: "inst-2", Identifiers: []db.IdentifierInput{{Type: "MIC_TICKER", Value: "XNAS:MSFT"}}},
		},
	}.expect(mockDB)

	err := runCycle(ctx, mockDB, reg, nil, nil, tel, "run-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runCycle err = %v, want context.Canceled so the run is not stamped success", err)
	}

	// Both gap rows are written up front, which is what leaves a record of the
	// instrument the cycle never got to.
	if len(rec.gaps) != 2 {
		t.Fatalf("recorded %d gaps, want 2", len(rec.gaps))
	}
	if got := rec.outcomes[gapID(1)]; got != db.TelemetryGapFilled {
		t.Errorf("first gap outcome = %q, want %q", got, db.TelemetryGapFilled)
	}
	if got, ok := rec.outcomes[gapID(2)]; ok {
		t.Errorf("second gap was stamped %q; a gap the cycle never reached stays unstamped", got)
	}
}

// cancelStub cancels the cycle as its first fetch returns, standing in for a
// container killed part way through.
type cancelStub struct {
	fetchStub
	cancel context.CancelFunc
}

func (s *cancelStub) FetchPrices(ctx context.Context, cfg []byte, ids []identifier.Identifier, ac string, from, before time.Time) (*FetchResult, error) {
	r, err := s.fetchStub.FetchPrices(ctx, cfg, ids, ac, from, before)
	s.cancel()
	return r, err
}
