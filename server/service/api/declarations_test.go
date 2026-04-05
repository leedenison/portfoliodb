package api

import (
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
)

func TestListHoldingDeclarations_Success(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	mockDB.EXPECT().
		ListHoldingDeclarations(gomock.Any(), "user-1").
		Return([]*db.HoldingDeclarationRow{
			{ID: "d1", UserID: "user-1", Broker: "IBKR", Account: "acct1", InstrumentID: "inst-1", DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)},
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
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any()).Return(float64(30), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		CreateDeclarationWithInitializeTx(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", "100", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), "BUYOTHER", gomock.Any(), float64(70)).
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
		DeclaredQty: "100", AsOfDate: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	mockDB.EXPECT().GetHoldingDeclaration(gomock.Any(), "d1").Return(existing, nil)
	startDate := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	mockDB.EXPECT().GetPortfolioStartDate(gomock.Any(), "user-1").Return(&startDate, nil)
	mockDB.EXPECT().ComputeRunningBalance(gomock.Any(), "user-1", "IBKR", "acct1", "inst-1", gomock.Any(), gomock.Any()).Return(float64(50), nil)
	mockDB.EXPECT().GetInstrument(gomock.Any(), "inst-1").Return(nil, nil)
	mockDB.EXPECT().
		UpdateDeclarationWithInitializeTx(gomock.Any(), "d1", "200", time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC), "user-1", "IBKR", "acct1", "inst-1", "BUYOTHER", gomock.Any(), float64(150)).
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
	mockDB.EXPECT().DeleteDeclarationWithInitializeTx(gomock.Any(), "d1", "user-1", "IBKR", "acct1", "inst-1").Return(nil)

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
