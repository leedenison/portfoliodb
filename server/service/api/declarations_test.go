package api

import (
	"fmt"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
)

var (
	asOfJun1 = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// storedBasis is a denomination the declaration already carries, distinct from
	// its as_of_date, so a test can tell "kept what was stored" from "re-derived".
	storedBasis = time.Date(2025, 8, 5, 0, 0, 0, 0, time.UTC)
)

func TestListHoldingDeclarations_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	mockDB.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return([]*db.HoldingDeclarationRow{
			{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "100", AsOfDate: asOfJun1, ShareCountBasis: storedBasis},
		}, nil)
	mockDB.EXPECT().ListInstrumentsByIDs(gomock.Any(), []string{"inst-1"}).Return(nil, nil)

	resp, err := srv.ListHoldingDeclarations(ctx, &apiv1.ListHoldingDeclarationsRequest{})
	if err != nil {
		t.Fatalf("ListHoldingDeclarations: %v", err)
	}
	if len(resp.GetDeclarations()) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(resp.GetDeclarations()))
	}
	d := resp.GetDeclarations()[0]
	if d.GetBroker() != "IBKR" || d.GetDeclaredQty() != "100" || d.GetAsOfDate().GetYear() != 2025 || d.GetAsOfDate().GetMonth() != 6 || d.GetAsOfDate().GetDay() != 1 {
		t.Fatalf("unexpected declaration: %+v", d)
	}
	// The denomination is part of what the declaration says, so it has to reach the
	// client: a quantity read without it is in an unknown share count.
	if b := d.GetShareCountBasis(); b.GetYear() != 2025 || b.GetMonth() != 8 || b.GetDay() != 5 {
		t.Fatalf("share_count_basis: want 2025-08-05, got %+v", b)
	}
}

func TestListHoldingDeclarations_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListHoldingDeclarations(ctxNoAuth(), &apiv1.ListHoldingDeclarationsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestCreateHoldingDeclaration_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
	// Unset in the request, so the balance is asked for at as_of_date -- as-traded.
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), asOfJun1).Return(decimal.NewFromInt(30), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "100", asOfJun1, asOfJun1, padEq("BUYOTHER", "70", asOfJun1)).
		Return(&db.HoldingDeclarationRow{
			ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "100",
			AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		}, nil)

	resp, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "100", AsOfDate: &date.Date{Year: 2025, Month: 6, Day: 1},
	})
	if err != nil {
		t.Fatalf("CreateHoldingDeclaration: %v", err)
	}
	if resp.GetDeclaration().GetId() != "d1" {
		t.Fatalf("unexpected id: %s", resp.GetDeclaration().GetId())
	}
}

// TestCreateHoldingDeclaration_StatedShareCountBasis covers the other reading of a
// declared quantity: a number copied off today's holdings screen rather than off a
// statement of as_of_date. Both are reasonable, so the request says which, and
// everything downstream of it -- the balance it is compared against and the pad it
// produces -- is denominated the same way.
func TestCreateHoldingDeclaration_StatedShareCountBasis(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), storedBasis).Return(decimal.NewFromInt(30), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "100", asOfJun1, storedBasis, padEq("BUYOTHER", "70", storedBasis)).
		Return(&db.HoldingDeclarationRow{ID: "d1", UserID: "user-1", AsOfDate: asOfJun1, ShareCountBasis: storedBasis}, nil)

	_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "100",
		AsOfDate:        &date.Date{Year: 2025, Month: 6, Day: 1},
		ShareCountBasis: &date.Date{Year: 2025, Month: 8, Day: 5},
	})
	if err != nil {
		t.Fatalf("CreateHoldingDeclaration: %v", err)
	}
}

// TestCreateHoldingDeclaration_LaterIsAnAssertion covers the second declaration for
// a holding. The earlier one keeps the pad, so the pad is recomputed from it rather
// than from the row being written, and the new one generates nothing.
func TestCreateHoldingDeclaration_LaterIsAnAssertion(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	padAsOf := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	assertAsOf := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "500", AsOfDate: padAsOf, ShareCountBasis: padAsOf},
	}, nil)
	// Balanced and padded at the existing declaration's date, not the new one's.
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), padAsOf).Return(decimal.NewFromInt(100), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "650", assertAsOf, assertAsOf, padEq("BUYOTHER", "400", padAsOf)).
		Return(&db.HoldingDeclarationRow{ID: "d2", UserID: "user-1", AsOfDate: assertAsOf, ShareCountBasis: assertAsOf}, nil)

	resp, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "650",
		AsOfDate: &date.Date{Year: 2023, Month: 12, Day: 31},
	})
	if err != nil {
		t.Fatalf("CreateHoldingDeclaration: %v", err)
	}
	if got := resp.GetDeclaration().GetKind(); got != apiv1.DeclarationKind_DECLARATION_KIND_ASSERT {
		t.Fatalf("kind: want ASSERT, got %v", got)
	}
}

// TestCreateHoldingDeclaration_EarlierTakesOverThePad is the same case from the
// other side: a declaration earlier than the current pad becomes the pad, and the
// one that used to hold it becomes an assertion.
func TestCreateHoldingDeclaration_EarlierTakesOverThePad(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	oldPad := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)
	newPad := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "650", AsOfDate: oldPad, ShareCountBasis: oldPad},
	}, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), newPad).Return(decimal.NewFromInt(100), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "500", newPad, newPad, padEq("BUYOTHER", "400", newPad)).
		Return(&db.HoldingDeclarationRow{ID: "d2", UserID: "user-1", AsOfDate: newPad, ShareCountBasis: newPad}, nil)

	resp, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "500",
		AsOfDate: &date.Date{Year: 2021, Month: 1, Day: 1},
	})
	if err != nil {
		t.Fatalf("CreateHoldingDeclaration: %v", err)
	}
	if got := resp.GetDeclaration().GetKind(); got != apiv1.DeclarationKind_DECLARATION_KIND_PAD {
		t.Fatalf("kind: want PAD, got %v", got)
	}
}

// TestCreateHoldingDeclaration_SameDate rejects a second declaration at a date the
// holding already has. The two would say different things about one moment, and
// nothing decides between them.
func TestCreateHoldingDeclaration_SameDate(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), asOfJun1).Return(decimal.NewFromInt(30), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "100", asOfJun1, asOfJun1, gomock.Any()).
		Return(nil, fmt.Errorf("create holding declaration: %w", db.ErrDuplicate))

	_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "100",
		AsOfDate: &date.Date{Year: 2025, Month: 6, Day: 1},
	})
	testutil.RequireGRPCCode(t, err, codes.AlreadyExists)
}

// TestDeleteHoldingDeclaration_PromotesTheNextPad covers deleting the pad while
// later declarations remain: the holding keeps a pad, rewritten from whichever
// declaration is now earliest.
func TestDeleteHoldingDeclaration_PromotesTheNextPad(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	oldPad := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	next := time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)

	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(&db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", AsOfDate: oldPad, ShareCountBasis: oldPad,
	}, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return([]*db.HoldingDeclarationRow{
		{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "500", AsOfDate: oldPad, ShareCountBasis: oldPad},
		{ID: "d2", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "650", AsOfDate: next, ShareCountBasis: next},
	}, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), next).Return(decimal.NewFromInt(50), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		DeleteDeclarationWithInitializeTx(gomock.Any(), "d1", "user-1", "IBKR", "acct1", "inst-1", padEq("BUYOTHER", "600", next)).
		Return(nil)

	if _, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{Id: "d1"}); err != nil {
		t.Fatalf("DeleteHoldingDeclaration: %v", err)
	}
}

func TestCreateHoldingDeclaration_NoRealTxs(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)

	_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "100", AsOfDate: &date.Date{Year: 2025, Month: 6, Day: 1},
	})
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestCreateHoldingDeclaration_DateBeforeStart(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	startDate := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)

	_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{
		Broker: "IBKR", Account: "acct1", InstrumentId: "inst-1", DeclaredQty: "100", AsOfDate: &date.Date{Year: 2025, Month: 1, Day: 1},
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestCreateHoldingDeclaration_MissingFields(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpdateHoldingDeclaration_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	existing := &db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
		DeclaredQty: "100", AsOfDate: asOfJun1, ShareCountBasis: storedBasis,
	}
	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(existing, nil)
	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ListHoldingDeclarations(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any(), storedBasis).Return(decimal.NewFromInt(50), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		UpdateDeclarationWithInitializeTx(gomock.Any(), "d1", "200", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), storedBasis, "user-1", "IBKR", "acct1", "inst-1", padEq("BUYOTHER", "150", storedBasis)).
		Return(&db.HoldingDeclarationRow{
			ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
			DeclaredQty: "200", AsOfDate: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		}, nil)

	resp, err := srv.UpdateHoldingDeclaration(ctx, &apiv1.UpdateHoldingDeclarationRequest{
		Id: "d1", DeclaredQty: "200", AsOfDate: &date.Date{Year: 2025, Month: 7, Day: 1},
	})
	if err != nil {
		t.Fatalf("UpdateHoldingDeclaration: %v", err)
	}
	if resp.GetDeclaration().GetDeclaredQty() != "200" {
		t.Fatalf("expected qty 200, got %s", resp.GetDeclaration().GetDeclaredQty())
	}
}

func TestUpdateHoldingDeclaration_NotOwner(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	existing := &db.HoldingDeclarationRow{ID: "d1", UserID: "user-2"}
	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(existing, nil)

	_, err := srv.UpdateHoldingDeclaration(ctx, &apiv1.UpdateHoldingDeclarationRequest{
		Id: "d1", DeclaredQty: "200", AsOfDate: &date.Date{Year: 2025, Month: 7, Day: 1},
	})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestDeleteHoldingDeclaration_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	existing := &db.HoldingDeclarationRow{
		ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1",
	}
	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(existing, nil)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(nil, nil)
	mockDB.EXPECT().DeleteDeclarationWithInitializeTx(gomock.Any(), "d1", "user-1", "IBKR", "acct1", "inst-1", gomock.Nil()).Return(nil)

	_, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{Id: "d1"})
	if err != nil {
		t.Fatalf("DeleteHoldingDeclaration: %v", err)
	}
}

func TestDeleteHoldingDeclaration_NotOwner(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	existing := &db.HoldingDeclarationRow{ID: "d1", UserID: "user-2"}
	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(existing, nil)

	_, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{Id: "d1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestDeleteHoldingDeclaration_MissingId(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}
