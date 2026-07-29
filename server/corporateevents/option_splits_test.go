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

// makeOption builds an InstrumentRow for an option with the given OCC, strike,
// and identity_as_of -- the point in market time the stored identity reflects.
func makeOption(id, occ string, strike float64, identityAsOf time.Time) *db.InstrumentRow {
	opt := makeOptionUnidentified(id, occ, strike)
	opt.IdentityAsOf = timePtr(identityAsOf)
	return opt
}

// makeOptionUnidentified builds an option whose identity_as_of is NULL, meaning
// the identity predates every split.
func makeOptionUnidentified(id, occ string, strike float64) *db.InstrumentRow {
	expiry := date(2025, 1, 17)
	putCall := "C"
	return &db.InstrumentRow{
		ID:      id,
		Strike:  floatPtr(strike),
		Expiry:  &expiry,
		PutCall: &putCall,
		Identifiers: []db.IdentifierInput{
			{Type: "OCC", Value: occ, Canonical: true},
		},
	}
}

// TestProcessOptionSplits_IdentityPredatesExDate verifies the identity was
// derived before the split took effect, so the stored OCC is the pre-split one
// and the adjustment must be applied.
func TestProcessOptionSplits_IdentityPredatesExDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-1111"
	underlyingID := "und-2222"

	// Identity as of Jan 1, split effective Jan 15 → identity predates it → apply.
	opt := makeOption(optID, "AAPL  250117C00200000", 200.0, date(2025, 1, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 2, 1),
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

// TestProcessOptionSplits_IdentityAfterExDate verifies the identity was derived
// after the split took effect, so the stored OCC is already the post-split one
// and must be left alone.
func TestProcessOptionSplits_IdentityAfterExDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-3333"
	underlyingID := "und-4444"

	// Identity as of Mar 1, split effective Jan 15 → already reflects it → skip.
	opt := makeOption(optID, "AAPL250117C00100000", 100.0, date(2025, 3, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	// ApplyOptionSplit must NOT be called.

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}

// TestProcessOptionSplits_IdentityOnExDate pins the inclusive boundary: an
// identity derived on the ex_date itself sees the adjusted contract, because the
// new terms apply from the open that day.
func TestProcessOptionSplits_IdentityOnExDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	underlyingID := "und-boundary"
	opt := makeOption("opt-boundary", "AAPL250117C00100000", 100.0, date(2025, 1, 15))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 1, 5),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	// ApplyOptionSplit must NOT be called.

	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, fixedTimer(date(2025, 3, 1)))
}

// TestProcessOptionSplits_KnownBeforeButNotYetEffective is the regression test
// for issue 0055. The split was known on Jan 5 and the option was identified on
// Mar 1 -- so under the old first_known_at guard it looked already-correct and
// was skipped forever. But the split does not take effect until Jun 1, so the
// identity derived in March carries the PRE-split OCC and does need adjusting.
func TestProcessOptionSplits_KnownBeforeButNotYetEffective(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	optID := "opt-0055"
	underlyingID := "und-0055"

	opt := makeOption(optID, "AAPL  250117C00200000", 200.0, date(2025, 3, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 6, 1),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 1, 5), // known well before the identity was derived
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.NewStrike != 100.0 {
				t.Errorf("new strike = %f, want 100", p.NewStrike)
			}
			return nil
		})

	// Now past the ex_date.
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, fixedTimer(date(2025, 7, 1)))
}

// TestProcessOptionSplits_NilIdentityAsOf verifies a NULL identity_as_of is
// treated as predating every split, so the adjustment applies.
func TestProcessOptionSplits_NilIdentityAsOf(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	underlyingID := "und-nil"
	opt := makeOptionUnidentified("opt-nil", "AAPL  250117C00200000", 200.0)

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return([]*db.InstrumentRow{opt}, nil)
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).Return(nil)

	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, fixedTimer(date(2025, 3, 1)))
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

	// Identity as of Jan 1, split effective Jun 1 (identity predates it).
	opt := makeOption(optID, "AAPL  250117C00400000", 400.0, date(2025, 1, 1))

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 6, 1), // future
		SplitFrom:    "1",
		SplitTo:      "4",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 1, 5),
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
		FirstKnownAt: date(2025, 1, 5),
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
				FirstKnownAt: date(2025, 2, 1),
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
		IdentityAsOf: timePtr(date(2025, 1, 1)),
		Identifiers:  []db.IdentifierInput{{Type: "MIC_TICKER", Value: "AAPL"}}, // no OCC
	}

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 2, 1),
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
		IdentityAsOf: timePtr(date(2025, 1, 1)),
		Identifiers:  []db.IdentifierInput{{Type: "OCC", Value: "NOTAVALIDOCC", Canonical: true}},
	}

	split := db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       date(2025, 1, 15),
		SplitFrom:    "1",
		SplitTo:      "2",
		DataProvider: "eodhd",
		FirstKnownAt: date(2025, 2, 1),
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
		FirstKnownAt: date(2025, 2, 1),
	}

	mockDB.EXPECT().ListOptionsByUnderlying(gomock.Any(), underlyingID).Return(nil, nil)

	timer := fixedTimer(date(2025, 3, 1))
	ProcessOptionSplits(ctx, mockDB, underlyingID, []db.StockSplit{split}, nil, timer)
}
