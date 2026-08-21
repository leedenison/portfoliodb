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

// makeOption builds an InstrumentRow for an option whose OCC symbol became
// correct on validFrom -- the vintage of whatever supplied it, or the ex_date of
// a split that minted it.
func makeOption(id, occ string, strike float64, validFrom time.Time) *db.InstrumentRow {
	opt := makeOptionUnidentified(id, occ, strike)
	opt.Identifiers[0].ValidFrom = timePtr(validFrom)
	return opt
}

// makeOptionUnidentified builds an option whose OCC has no valid_from, meaning
// the name predates everything known about it.
func makeOptionUnidentified(id, occ string, strike float64) *db.InstrumentRow {
	expiry := date(2025, 1, 17)
	putCall := "C"
	return &db.InstrumentRow{
		ID:      id,
		Strike:  decPtr(strike),
		Expiry:  &expiry,
		PutCall: &putCall,
		Identifiers: []db.IdentifierInput{
			{
				Ref:       db.InstrumentRef{Type: "OCC", Value: occ},
				Canonical: true,
			}},
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
// server/db/postgres for the OCC row's valid_from against ex_date. These tests
// cover what the pass does with the work list it is handed.

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
			if len(p.Mints) != 1 {
				t.Fatalf("mints = %d, want 1", len(p.Mints))
			}
			if p.Mints[0].OCC.Ref.Value != "AAPL250117C00100000" {
				t.Errorf("minted OCC = %q, want AAPL250117C00100000", p.Mints[0].OCC.Ref.Value)
			}
			if p.Mints[0].Strike.String() != "100" {
				t.Errorf("minted strike = %v, want 100", p.Mints[0].Strike)
			}
			if !p.Mints[0].ExDate.Equal(date(2025, 1, 15)) {
				t.Errorf("minted from %v, want the ex_date 2025-01-15", p.Mints[0].ExDate)
			}
			return nil
		})

	adjusted := ProcessPendingOptionSplits(ctx, mockDB, "und-2222", nil)
	if len(adjusted) != 1 {
		t.Fatalf("adjusted = %d options, want 1", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_CompoundsMultipleSplits covers what two pending
// splits produce: one name per split, each derived from the option's stored
// strike by the cumulative factor up to that split, so no rounded value feeds
// another. The names in between are minted rather than skipped -- a file
// exported in that window states one, and without the row it would have nothing
// to resolve to.
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

	// One call carrying both names: 400/2 = 200 from the first ex_date, and
	// 400/(2*4) = 50 from the second.
	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if len(p.Mints) != 2 {
				t.Fatalf("mints = %d, want one per split", len(p.Mints))
			}
			want := []struct {
				exDate time.Time
				occ    string
				strike string
			}{
				{date(2024, 6, 1), "AAPL250117C00200000", "200"},
				{date(2024, 9, 1), "AAPL250117C00050000", "50"},
			}
			for i, w := range want {
				if !p.Mints[i].ExDate.Equal(w.exDate) {
					t.Errorf("mint %d ex_date = %v, want %v", i, p.Mints[i].ExDate, w.exDate)
				}
				if p.Mints[i].OCC.Ref.Value != w.occ {
					t.Errorf("mint %d OCC = %q, want %q", i, p.Mints[i].OCC.Ref.Value, w.occ)
				}
				if p.Mints[i].Strike.String() != w.strike {
					t.Errorf("mint %d strike = %v, want %v", i, p.Mints[i].Strike, w.strike)
				}
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

// TestProcessPendingOptionSplits_RestatesFromTheNameInForce: a restated option
// keeps the symbol it traded under before the ex_date, so the row set has more
// than one OCC in it. Building the next name from a closed one would restate a
// split that had already been applied.
func TestProcessPendingOptionSplits_RestatesFromTheNameInForce(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)
	ctx := context.Background()

	// Already restated once for a 2:1: the 400-strike name is closed and the
	// 200-strike one is in force. The closed row is listed first to make the
	// point that order is not what decides it.
	opt := makeOption("opt-history", "AAPL250117C00200000", 200.0, date(2024, 6, 1))
	opt.Identifiers = append([]db.IdentifierInput{{
		Ref:         db.InstrumentRef{Type: "OCC", Value: "AAPL250117C00400000"},
		Canonical:   true,
		ValidBefore: timePtr(date(2024, 6, 1)),
	}}, opt.Identifiers...)

	mockDB.EXPECT().ListPendingOptionSplits(gomock.Any(), "").Return(
		[]db.PendingOptionSplits{{
			Option: opt,
			Splits: []db.StockSplit{split("und-history", date(2024, 9, 1), "1", "2")},
		}}, nil)

	mockDB.EXPECT().ApplyOptionSplit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p db.OptionSplitParams) error {
			if p.OldOCCValue != "AAPL250117C00200000" {
				t.Errorf("old OCC = %q, want the one in force", p.OldOCCValue)
			}
			if len(p.Mints) != 1 || p.Mints[0].OCC.Ref.Value != "AAPL250117C00100000" {
				t.Errorf("mints = %+v, want just AAPL250117C00100000", p.Mints)
			}
			return nil
		})

	if adjusted := ProcessPendingOptionSplits(ctx, mockDB, "", nil); len(adjusted) != 1 {
		t.Fatalf("adjusted = %d options, want 1", len(adjusted))
	}
}

// TestProcessPendingOptionSplits_ApplyFailureNotReported verifies a failed
// restatement is not counted as done. The OCC symbol in force is closed inside
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
// and leaving the OCC symbol in force where it is keeps the option pending for a
// later run.
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
	opt.Identifiers = []db.IdentifierInput{{
		Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
		Canonical: true,
	}}
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

func TestIsWholeForwardSplit(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{"1", "2", true},
		{"1", "10", true},
		{"1", "4", true},
		{"2", "1", false}, // reverse
		{"2", "3", false}, // non-whole
		{"1", "1", false}, // no change
		{"0", "2", false}, // invalid
		{"", "2", false},  // invalid
		{"1", "", false},  // invalid
	}
	for _, tt := range tests {
		t.Run(tt.from+":"+tt.to, func(t *testing.T) {
			got := IsWholeForwardSplit(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("IsWholeForwardSplit(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
