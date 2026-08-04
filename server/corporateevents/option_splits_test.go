package corporateevents

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
)

// decPtr builds a strike from a float literal, which is what a test fixture is
// naturally written as. Production code never converts this way.
func decPtr(f float64) *decimal.Decimal { d := decimal.NewFromFloat(f); return &d }

func timePtr(t time.Time) *time.Time { return &t }

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
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
		Strike:  decPtr(strike),
		Expiry:  &expiry,
		PutCall: &putCall,
		Identifiers: []db.IdentifierInput{
			{Type: "OCC", Value: occ, Canonical: true},
		},
	}
}

func split(underlyingID string, exDate time.Time, from, to string) db.StockSplit {
	return db.StockSplit{
		InstrumentID: underlyingID,
		ExDate:       exDate,
		SplitFrom:    from,
		SplitTo:      to,
		DataProvider: "eodhd",
		FirstKnownAt: exDate,
	}
}

// Which splits are pending for an option is decided by the SQL predicate in
// ListPendingOptionSplits, not by this package -- see the integration tests in
// server/db/postgres for identity_as_of against ex_date. These tests cover what
// the pass does with the work list it is handed.

func TestProcessPendingOptionSplits_SingleSplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	opt := makeOption("opt-1111", "AAPL  250117C00200000", 200.0, date(2025, 1, 1))
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "und-2222").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{split("und-2222", date(2025, 1, 15), "1", "2")},
		}}, nil)

	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.InstrumentID != "opt-1111" {
				t.Errorf("instrument id = %q, want opt-1111", p.InstrumentID)
			}
			if p.OldOCCValue != "AAPL  250117C00200000" {
				t.Errorf("old OCC = %q", p.OldOCCValue)
			}
			if p.NewOCC.Value != "AAPL250117C00100000" {
				t.Errorf("new OCC = %q, want AAPL250117C00100000", p.NewOCC.Value)
			}
			if p.NewStrike.String() != "100" {
				t.Errorf("new strike = %v, want 100", p.NewStrike)
			}
			return nil
		})

	adjusted := ProcessPendingOptionSplits(ctx, mockDB, "und-2222", nil)
	if len(adjusted) != 1 {
		t.Fatalf("adjusted = %d options, want 1", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_CompoundsMultipleSplits covers the bug the
// per-split loop had: the option row is read once, so applying two splits in
// sequence divided the ORIGINAL strike twice and inserted an OCC identifier per
// split, leaving the option carrying both. Adjusting once by the cumulative
// factor is correct by construction.
func TestProcessPendingOptionSplits_CompoundsMultipleSplits(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	opt := makeOption("opt-multi", "AAPL  250117C00400000", 400.0, date(2024, 1, 1))
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{
				split("und-multi", date(2024, 6, 1), "1", "2"), // 2:1
				split("und-multi", date(2024, 9, 1), "1", "4"), // 4:1
			},
		}}, nil)

	// Exactly one call: strike 400 / (2 * 4) = 50.
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.NewStrike.String() != "50" {
				t.Errorf("new strike = %v, want 50 (400 / (2*4))", p.NewStrike)
			}
			if p.NewOCC.Value != "AAPL250117C00050000" {
				t.Errorf("new OCC = %q, want AAPL250117C00050000", p.NewOCC.Value)
			}
			if p.OldOCCValue != "AAPL  250117C00400000" {
				t.Errorf("old OCC = %q, want the original", p.OldOCCValue)
			}
			return nil
		}).Times(1)

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 1 {
		t.Fatalf("adjusted = %d options, want 1", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_ApplyFailureNotReported verifies a failed
// adjustment is not counted as done. identity_as_of is advanced inside
// ApplyOptionSplit's transaction, so a failure leaves the option pending and the
// next cycle retries it -- the retry half of issue 0055.
func TestProcessPendingOptionSplits_ApplyFailureNotReported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	opt := makeOption("opt-fail", "AAPL  250117C00200000", 200.0, date(2025, 1, 1))
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{split("und-fail", date(2025, 1, 15), "1", "2")},
		}}, nil)
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).Return(errors.New("deadlock"))

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 0 {
		t.Errorf("adjusted = %d options, want 0 after a failed apply", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_OneFailureDoesNotBlockOthers verifies the pass
// keeps going after a per-option failure.
func TestProcessPendingOptionSplits_OneFailureDoesNotBlockOthers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	bad := makeOption("opt-bad", "AAPL  250117C00200000", 200.0, date(2025, 1, 1))
	good := makeOption("opt-good", "AAPL  250117C00300000", 300.0, date(2025, 1, 1))
	s := []db.StockSplit{split("und-x", date(2025, 1, 15), "1", "2")}
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{Option: bad, Splits: s}, {Option: good, Splits: s}}, nil)

	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.InstrumentID == "opt-bad" {
				return errors.New("deadlock")
			}
			return nil
		}).Times(2)

	adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil)
	if len(adjusted) != 1 || adjusted[0].ID != "opt-good" {
		t.Errorf("adjusted = %v, want just opt-good", adjusted)
	}
}

// TestProcessPendingOptionSplits_NonWholeSplitBlocksOption verifies a split we
// cannot apply stops the whole option rather than being skipped over. Applying
// the splits either side of it would produce a strike matching no real contract,
// and leaving identity_as_of untouched keeps the option pending for a later run.
func TestProcessPendingOptionSplits_NonWholeSplitBlocksOption(t *testing.T) {
	tests := []struct {
		name      string
		from, to  string
		eventType string
	}{
		{"reverse split", "2", "1", "REVERSE_SPLIT"},
		{"fractional split", "2", "3", "NON_WHOLE_SPLIT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockDB := mock.NewMockDB(ctrl)
			ctx := context.Background()

			opt := makeOption("opt-nw", "AAPL  250117C00200000", 200.0, date(2025, 1, 1))
			mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
				[]db.PendingOptionSplits{{
					Option: opt,
					Splits: []db.StockSplit{
						split("und-nw", date(2025, 1, 15), tc.from, tc.to),
						// A whole split alongside it must NOT be applied either.
						split("und-nw", date(2025, 2, 1), "1", "2"),
					},
				}}, nil)

			// No ApplyOptionSplit: the option is blocked.
			mockDB.EXPECT().InsertUnhandledCorporateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, e db.UnhandledCorporateEvent) error {
					if e.InstrumentID != "und-nw" {
						t.Errorf("event instrument = %q, want the underlying und-nw", e.InstrumentID)
					}
					if e.EventType != tc.eventType {
						t.Errorf("event type = %q, want %q", e.EventType, tc.eventType)
					}
					return nil
				})

			if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 0 {
				t.Errorf("adjusted = %d options, want 0", len(adjusted))
			}
		})
	}
}

// TestProcessPendingOptionSplits_NonWholeSplitReportedOncePerSplit verifies the
// unhandled event is raised once for the underlying, listing every option it
// blocks, rather than once per option.
func TestProcessPendingOptionSplits_NonWholeSplitReportedOncePerSplit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	s := []db.StockSplit{split("und-shared", date(2025, 1, 15), "2", "3")}
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{
			{Option: makeOption("opt-a", "AAPL  250117C00200000", 200.0, date(2025, 1, 1)), Splits: s},
			{Option: makeOption("opt-b", "AAPL  250117C00300000", 300.0, date(2025, 1, 1)), Splits: s},
		}, nil)

	mockDB.EXPECT().InsertUnhandledCorporateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e db.UnhandledCorporateEvent) error {
			if !strings.Contains(string(e.Data), "opt-a") || !strings.Contains(string(e.Data), "opt-b") {
				t.Errorf("event data %s should list both blocked options", e.Data)
			}
			return nil
		}).Times(1)

	ProcessPendingOptionSplits(ctx, mockDB, "", nil)
}

func TestProcessPendingOptionSplits_NoOCC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	opt := makeOption("opt-noocc", "", 200.0, date(2025, 1, 1))
	opt.Identifiers = []db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true}}
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{split("und-noocc", date(2025, 1, 15), "1", "2")},
		}}, nil)
	// No ApplyOptionSplit and no unhandled event: nothing to rewrite.

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 0 {
		t.Errorf("adjusted = %d options, want 0", len(adjusted))
	}
}

func TestProcessPendingOptionSplits_UnparseableOCC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	opt := makeOption("opt-bad-occ", "NOTAVALIDOCC", 200.0, date(2025, 1, 1))
	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{split("und-bad", date(2025, 1, 15), "1", "2")},
		}}, nil)

	mockDB.EXPECT().InsertUnhandledCorporateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e db.UnhandledCorporateEvent) error {
			if e.InstrumentID != "opt-bad-occ" {
				t.Errorf("event instrument = %q, want the option", e.InstrumentID)
			}
			return nil
		})

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 0 {
		t.Errorf("adjusted = %d options, want 0", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_NothingPending is the steady state: running the
// pass on every cycle when there is no work must touch nothing.
func TestProcessPendingOptionSplits_NothingPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(nil, nil)

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 0 {
		t.Errorf("adjusted = %d options, want 0", len(adjusted))
	}
}

func TestProcessPendingOptionSplits_ListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(nil, errors.New("connection refused"))

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); adjusted != nil {
		t.Errorf("adjusted = %v, want nil on a query error", adjusted)
	}
}
