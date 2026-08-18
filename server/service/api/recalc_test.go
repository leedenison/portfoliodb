package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
)

// padEq matches an expected pad by quantity and denomination. The quantity is
// taken as a decimal string for the reason testutil.DecEq exists, and the basis is
// checked because a pad in the wrong share count is wrong by a split factor while
// looking entirely plausible.
func padEq(qty string, basis time.Time) gomock.Matcher {
	return padMatcher{qty: decimal.RequireFromString(qty), basis: basis}
}

type padMatcher struct {
	qty   decimal.Decimal
	basis time.Time
}

func (m padMatcher) Matches(x any) bool {
	got, ok := x.(db.InitializeTx)
	if !ok {
		p, isPtr := x.(*db.InitializeTx)
		if !isPtr || p == nil {
			return false
		}
		got = *p
	}
	return got.Quantity.Equal(m.qty) && got.ShareCountBasis.Equal(m.basis)
}

func (m padMatcher) String() string {
	return fmt.Sprintf("is a pad of %s denominated at %s", m.qty, m.basis.Format("2006-01-02"))
}

var (
	testAsOf  = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	testBasis = time.Date(2025, 8, 5, 0, 0, 0, 0, time.UTC)
)

func testDecl(id, instrumentID, declaredQty string) *db.HoldingDeclarationRow {
	return &db.HoldingDeclarationRow{
		ID: id, UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: instrumentID,
		DeclaredQty: declaredQty, AsOfDate: testAsOf, ShareCountBasis: testBasis,
	}
}

// declAt is testDecl at a chosen date, for the cases about which declaration pads.
func declAt(id, instrumentID, declaredQty string, asOf time.Time) *db.HoldingDeclarationRow {
	d := testDecl(id, instrumentID, declaredQty)
	d.AsOfDate = asOf
	d.ShareCountBasis = asOf
	return d
}

func TestRecalcAllInitializeTxs_RecomputesQty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{testDecl("d1", "inst-1", "100")}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	// The balance is asked for in the declaration's denomination, not in whatever
	// mixture the postings happen to be recorded in.
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(40), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("60", testBasis)).Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

// TestRecalcAllInitializeTxs_PadsTheEarliest is the rule the whole pad/assert split
// rests on: a holding's earliest declaration seeds its opening balance, and the
// later ones generate nothing at all.
func TestRecalcAllInitializeTxs_PadsTheEarliest(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	pad := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	assert := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	startDate := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)

	// Listed newest first, so the choice cannot come from the ordering.
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		declAt("d2", "inst-1", "650", assert),
		declAt("d1", "inst-1", "500", pad),
	}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	// Exactly one balance and one upsert: the assertion is not padded to.
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), pad).Return(decimal.NewFromInt(100), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("400", pad)).Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

func TestRecalcAllInitializeTxs_NoRealTxs_DeletesInitialize(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{testDecl("d1", "inst-1", "100")}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)
	// The declaration is what the user said and survives; only the derived pad goes.
	mockDB.EXPECT().DeleteInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1").Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

// TestRecalcAllInitializeTxs_StartDatePastDeclaration_PromotesTheNext covers a start
// date that moved forward past the pad. The pad's declaration is about a date the
// history no longer reaches and goes; the assertion behind it becomes the pad.
func TestRecalcAllInitializeTxs_StartDatePastDeclaration_PromotesTheNext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	old := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	next := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	startDate := time.Date(2022, 1, 1, 10, 0, 0, 0, time.UTC)

	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		declAt("d1", "inst-1", "500", old),
		declAt("d2", "inst-1", "650", next),
	}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().DeleteHoldingDeclaration(gomock.Any(), "d1").Return(nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), next).Return(decimal.NewFromInt(50), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("600", next)).Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

// TestRecalcAllInitializeTxs_StartDatePastAll_DeletesTheHoldingsPad is the same
// pruning with nothing left behind it, so the holding loses its pad as well.
func TestRecalcAllInitializeTxs_StartDatePastAll_DeletesTheHoldingsPad(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{testDecl("d1", "inst-1", "100")}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().DeleteHoldingDeclaration(gomock.Any(), "d1").Return(nil)
	mockDB.EXPECT().DeleteInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1").Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

// TestRecalcAllInitializeTxs_ZeroQty_KeepsDeclaration covers the case where the real
// transactions already account for the declared balance exactly. That is the
// declaration agreeing with the data, which is the outcome a checked declaration
// exists to report -- so the record survives and the pad is written as zero.
func TestRecalcAllInitializeTxs_ZeroQty_KeepsDeclaration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{testDecl("d1", "inst-1", "100")}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(100), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("0", testBasis)).Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

func TestRecalcAllInitializeTxs_RecalcsEachHolding(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		testDecl("d1", "inst-1", "100"),
		testDecl("d2", "inst-2", "50"),
	}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(20), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("80", testBasis)).Return(nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(10), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", padEq("40", testBasis)).Return(nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}

func TestRecalcAllInitializeTxs_NoDeclarations_Noop(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)

	if err := RecalcAllInitializeTxs(context.Background(), mockDB, "user-1"); err != nil {
		t.Fatalf("RecalcAllInitializeTxs: %v", err)
	}
}
