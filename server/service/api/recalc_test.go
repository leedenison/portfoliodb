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

// padEq matches an expected pad by type, quantity and denomination. The quantity is
// taken as a decimal string for the reason testutil.DecEq exists, and the basis is
// checked because a pad in the wrong share count is wrong by a split factor while
// looking entirely plausible.
func padEq(txType, qty string, basis time.Time) gomock.Matcher {
	return padMatcher{txType: txType, qty: decimal.RequireFromString(qty), basis: basis}
}

type padMatcher struct {
	txType string
	qty    decimal.Decimal
	basis  time.Time
}

func (m padMatcher) Matches(x any) bool {
	got, ok := x.(db.InitializeTx)
	return ok && got.TxType == m.txType && got.Quantity.Equal(m.qty) && got.ShareCountBasis.Equal(m.basis)
}

func (m padMatcher) String() string {
	return fmt.Sprintf("is a %s pad of %s denominated at %s", m.txType, m.qty, m.basis.Format("2006-01-02"))
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

func TestRecalcInitializeTx_RecomputesQty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	decl := testDecl("d1", "inst-1", "100")

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	// The balance is asked for in the declaration's denomination, not in whatever
	// mixture the postings happen to be recorded in.
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(40), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("BUYOTHER", "60", testBasis)).Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, decl); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcInitializeTx_NoRealTxs_DeletesInitialize(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().DeleteInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1").Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, testDecl("d1", "inst-1", "100")); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcInitializeTx_StartDatePastDeclaration_DeletesBoth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	// Start date moved to July, but declaration is for June
	startDate := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().DeleteDeclarationWithInitializeTx(gomock.Any(), "d1", "user-1", "IBKR", "acct1", "inst-1").Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, testDecl("d1", "inst-1", "100")); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

// TestRecalcInitializeTx_ZeroQty_KeepsDeclaration covers the case where the real
// transactions already account for the declared balance exactly. That is the
// declaration agreeing with the data, which is the outcome a checked declaration
// exists to report -- so the record survives and the pad is written as zero.
func TestRecalcInitializeTx_ZeroQty_KeepsDeclaration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(100), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("BUYOTHER", "0", testBasis)).Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, testDecl("d1", "inst-1", "100")); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcAllInitializeTxs_RecalcsEachDeclaration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	decls := []*db.HoldingDeclarationRow{
		testDecl("d1", "inst-1", "100"),
		testDecl("d2", "inst-2", "50"),
	}
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(decls, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	// Each declaration triggers a recalc
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil).Times(2)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(20), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", padEq("BUYOTHER", "80", testBasis)).Return(nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", gomock.Any(), gomock.Any(), testBasis).Return(decimal.NewFromInt(10), nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", padEq("BUYOTHER", "40", testBasis)).Return(nil)

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
