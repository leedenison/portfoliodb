package corporateevents

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/clock"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

func floatPtr(f float64) *float64 { return &f }
func timePtr(t time.Time) *time.Time { return &t }

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func fixedTimer(t time.Time) *clock.Timer {
	return &clock.Timer{NowFunc: func() time.Time { return t }}
}

// makeOption builds an InstrumentRow for an option with the given OCC,
// strike, and identification timestamp.
func makeOption(id, occ string, strike float64, identifiedAt time.Time) *db.InstrumentRow {
	expiry := date(2025, 1, 17)
	putCall := "C"
	return &db.InstrumentRow{
		ID:           id,
		Strike:       floatPtr(strike),
		Expiry:       &expiry,
		PutCall:      &putCall,
		IdentifiedAt: timePtr(identifiedAt),
		Identifiers: []db.IdentifierInput{
			{Type: "OCC", Value: occ, Canonical: true},
		},
	}
}

// TestProcessOptionSplits_TxBeforeSplit verifies case 1: option was
// identified before the split was fetched (identified_at < fetched_at).
// The split should be applied: OCC updated, strike adjusted, derived
// split created.
func TestProcessOptionSplits_TxBeforeSplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-1111"
	underlyingID := "und-2222"

	// Option identified at Jan 1, split fetched at Feb 1 → identified_at < fetched_at → apply.
	opt := makeOption(optID, "AAPL  250117C00200000", 200.0, date(2025, 1, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)

	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.InstrumentID != optID {
				t.Errorf("instrument id = %q, want %q", p.InstrumentID, optID)
			}
			if p.OldOCCValue != "AAPL  250117C00200000" {
				t.Errorf("old OCC = %q", p.OldOCCValue)
			}
			if p.NewOCC.Value != "AAPL250117C00100000" {
				t.Errorf("new OCC = %q, want AAPL250117C00100000", p.NewOCC.Value)
			}
			if p.NewStrike != 100.0 {
				t.Errorf("new strike = %f, want 100", p.NewStrike)
			}
			return nil
		})

	timer := fixedTimer(date(2025, 3, 1)) // after ex_date
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_SplitBeforeTx verifies case 2: option was
// identified after the split was fetched (identified_at >= fetched_at).
// The split should be skipped (case-3 guard: already correct).
func TestProcessOptionSplits_SplitBeforeTx(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-3333"
	underlyingID := "und-4444"

	// Option identified at Mar 1, split fetched at Feb 1 → identified_at >= fetched_at → skip.
	opt := makeOption(optID, "AAPL250117C00100000", 100.0, date(2025, 3, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	// ApplyOptionSplit must NOT be called.

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_FutureSplitSkipped verifies case 3: the split
// ex_date is in the future. The split should be skipped. After advancing
// time past the ex_date, the split should be applied.
func TestProcessOptionSplits_FutureSplitSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-5555"
	underlyingID := "und-6666"

	// Option identified Jan 1, split fetched Jan 5 (identified_at < fetched_at).
	opt := makeOption(optID, "AAPL  250117C00400000", 400.0, date(2025, 1, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 6, 1), // future
		SplitFrom:    "1",
		SplitTo:      "4",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 1, 5),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	// No ApplyOptionSplit expected: split is future-dated.

	timer := fixedTimer(date(2025, 3, 1)) // before ex_date
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_FutureThenAdvance verifies that after time
// advances past the ex_date, a previously future-dated split is applied.
func TestProcessOptionSplits_FutureThenAdvance(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-7777"
	underlyingID := "und-8888"

	opt := makeOption(optID, "AAPL  250117C00400000", 400.0, date(2025, 1, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 6, 1),
		SplitFrom:    "1",
		SplitTo:      "4",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 1, 5),
	}

	// First call: future-dated, skip. Second call: time advanced, apply.
	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil).Times(2)
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.NewOCC.Value != "AAPL250117C00100000" {
				t.Errorf("new OCC = %q, want AAPL250117C00100000", p.NewOCC.Value)
			}
			if p.NewStrike != 100.0 {
				t.Errorf("new strike = %f, want 100", p.NewStrike)
			}
			return nil
		})

	// Phase 1: before ex_date — no processing.
	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)

	// Phase 2: after ex_date — split applied.
	timer = fixedTimer(date(2025, 7, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_NonWholeForwardSplit verifies that non-standard
// splits (reverse or fractional) are routed to unhandled_corporate_events.
func TestProcessOptionSplits_NonWholeForwardSplit(t *testing.T) {
	tests := []struct {
		name      string
		from, to  string
		wantType  string
	}{
		{"reverse split", "2", "1", "REVERSE_SPLIT"},
		{"fractional split", "2", "3", "NON_WHOLE_SPLIT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDB := mock.NewMockDB(ctrl)
			ctx := context.Background()

			underlyingID := "und-9999"
			opt := makeOption("opt-aaaa", "AAPL  250117C00200000", 200.0, date(2025, 1, 1))

			split := db.StockSplit{
				InstrumentID: underlyingID,
				ExDate:       date(2025, 1, 15),
				SplitFrom:    tt.from,
				SplitTo:      tt.to,
				DataProvider: "eodhd",
				FetchedAt:    date(2025, 2, 1),
			}

			mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
			mockDB.EXPECT().InsertUnhandledCorporateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, ev db.UnhandledCorporateEvent) error {
					if ev.EventType != tt.wantType {
						t.Errorf("event type = %q, want %q", ev.EventType, tt.wantType)
					}
					if ev.InstrumentID != underlyingID {
						t.Errorf("instrument = %q, want %q", ev.InstrumentID, underlyingID)
					}
					return nil
				})

			timer := fixedTimer(date(2025, 3, 1))
			ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
		})
	}
}

// TestProcessOptionSplits_NoOCC verifies that options without an OCC
// identifier are skipped gracefully (no panic, no ApplyOptionSplit call).
func TestProcessOptionSplits_NoOCC(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	underlyingID := "und-bbbb"
	expiry := date(2025, 1, 17)
	putCall := "C"
	opt := &db.InstrumentRow{
		ID:           "opt-cccc",
		Strike:       floatPtr(200.0),
		Expiry:       &expiry,
		PutCall:      &putCall,
		IdentifiedAt: timePtr(date(2025, 1, 1)),
		Identifiers:  []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}}, // no OCC
	}

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	// No ApplyOptionSplit or InsertUnhandledCorporateEvent expected.

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_UnparseableOCC verifies that options with a
// malformed OCC identifier produce an unhandled corporate event.
func TestProcessOptionSplits_UnparseableOCC(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	underlyingID := "und-dddd"
	optID := "opt-eeee"
	expiry := date(2025, 1, 17)
	putCall := "C"
	opt := &db.InstrumentRow{
		ID:           optID,
		Strike:       floatPtr(200.0),
		Expiry:       &expiry,
		PutCall:      &putCall,
		IdentifiedAt: timePtr(date(2025, 1, 1)),
		Identifiers:  []db.IdentifierInput{{Type: "OCC", Value: "NOTAVALIDOCC", Canonical: true}},
	}

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	mockDB.EXPECT().InsertUnhandledCorporateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, ev db.UnhandledCorporateEvent) error {
			if ev.InstrumentID != optID {
				t.Errorf("instrument = %q, want %q", ev.InstrumentID, optID)
			}
			return nil
		})

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_NoOptions verifies that when no options exist
// on the underlying, the function returns cleanly with no DB calls.
func TestProcessOptionSplits_NoOptions(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	underlyingID := "und-ffff"
	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FetchedAt:    date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return(nil, nil)

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}
