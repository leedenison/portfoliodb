package api

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
)

func TestRecalcInitializeTx_RecomputesQty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	decl := &db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
		DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any()).Return(float64(40), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "BUYOTHER", gomock.Any(), float64(60)).Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, decl); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcInitializeTx_NoRealTxs_DeletesInitialize(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	decl := &db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
		DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().DeleteInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1").Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, decl); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcInitializeTx_StartDatePastDeclaration_DeletesBoth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	// Start date moved to July, but declaration is for June
	startDate := time.Date(2025, 7, 1, 10, 0, 0, 0, time.UTC)
	decl := &db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
		DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().DeleteInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1").Return(nil)
	mockDB.EXPECT().DeleteHoldingDeclaration(gomock.Any(), "d1").Return(nil)

	if err := RecalcInitializeTx(context.Background(), mockDB, decl); err != nil {
		t.Fatalf("RecalcInitializeTx: %v", err)
	}
}

func TestRecalcAllInitializeTxs_RecalcsEachDeclaration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockDB := mock.NewMockDB(ctrl)

	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	decls := []*db.HoldingDeclarationRow{
		{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "d2", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-2", DeclaredQty: "50", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(decls, nil)
	// Each declaration triggers a recalc
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil).Times(2)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any()).Return(float64(20), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "BUYOTHER", gomock.Any(), float64(80)).Return(nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", gomock.Any(), gomock.Any()).Return(float64(10), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-2").Return(nil, nil)
	mockDB.EXPECT().UpsertInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-2", "BUYOTHER", gomock.Any(), float64(40)).Return(nil)

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
