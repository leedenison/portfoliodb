package api

import (
	"errors"
	"testing"

	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetPortfolio_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestGetPortfolio_Internal(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, errors.New("db error"))
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestGetPortfolio_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	port := &apiv1.Portfolio{Id: "port-1", Name: "My Portfolio"}
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		GetPortfolio(gomock.Any(), "port-1").
		Return(port, "user-1", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("GetPortfolio: %v", err)
	}
	if resp.GetPortfolio().GetId() != "port-1" || resp.GetPortfolio().GetName() != "My Portfolio" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestCreatePortfolio_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	port := &apiv1.Portfolio{Id: "port-1", Name: "New Portfolio"}
	db.EXPECT().
		CreatePortfolio(gomock.Any(), "user-1", "New Portfolio").
		Return(port, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "New Portfolio"})
	if err != nil {
		t.Fatalf("CreatePortfolio: %v", err)
	}
	if resp.GetPortfolio().GetId() != "port-1" || resp.GetPortfolio().GetName() != "New Portfolio" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestCreatePortfolio_DBError(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		CreatePortfolio(gomock.Any(), "user-1", "x").
		Return(nil, errors.New("db error"))
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "x"})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestListPortfolios_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	portfolios := []*apiv1.Portfolio{{Id: "p1", Name: "P1"}}
	db.EXPECT().
		ListPortfolios(gomock.Any(), "user-1", int32(50), "").
		Return(portfolios, "", nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{})
	if err != nil {
		t.Fatalf("ListPortfolios: %v", err)
	}
	if len(resp.GetPortfolios()) != 1 || resp.GetPortfolios()[0].GetId() != "p1" {
		t.Fatalf("got %v", resp.GetPortfolios())
	}
}

func TestListPortfolios_PageSizeClamping(t *testing.T) {
	ctx := authCtx("user-1", "sub|1")
	t.Run("zero_clamps_to_50", func(t *testing.T) {
		srv, db := newAPIServerWithMock(t)
		db.EXPECT().
			ListPortfolios(gomock.Any(), "user-1", int32(50), "").
			Return(nil, "", nil)
		_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{PageSize: 0})
		if err != nil {
			t.Fatalf("ListPortfolios: %v", err)
		}
	})
	t.Run("over_100_clamps_to_100", func(t *testing.T) {
		srv, db := newAPIServerWithMock(t)
		db.EXPECT().
			ListPortfolios(gomock.Any(), "user-1", int32(100), "").
			Return(nil, "", nil)
		_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{PageSize: 200})
		if err != nil {
			t.Fatalf("ListPortfolios: %v", err)
		}
	})
}

func TestListPortfolios_DBError(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		ListPortfolios(gomock.Any(), "user-1", int32(50), "").
		Return(nil, "", errors.New("db error"))
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestUpdatePortfolio_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "port-1", Name: "x"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestUpdatePortfolio_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	port := &apiv1.Portfolio{Id: "port-1", Name: "Updated"}
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		UpdatePortfolio(gomock.Any(), "port-1", "Updated").
		Return(port, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "port-1", Name: "Updated"})
	if err != nil {
		t.Fatalf("UpdatePortfolio: %v", err)
	}
	if resp.GetPortfolio().GetName() != "Updated" {
		t.Fatalf("got %v", resp.GetPortfolio())
	}
}

func TestDeletePortfolio_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestDeletePortfolio_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		DeletePortfolio(gomock.Any(), "port-1").
		Return(nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("DeletePortfolio: %v", err)
	}
}

func TestGetPortfolioFilters_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ListPortfolioFilters(gomock.Any(), "port-1").
		Return([]dbpkg.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}}, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetPortfolioFilters(ctx, &apiv1.GetPortfolioFiltersRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("GetPortfolioFilters: %v", err)
	}
	if len(resp.GetFilters()) != 1 || resp.GetFilters()[0].GetFilterType() != "broker" || resp.GetFilters()[0].GetFilterValue() != "IBKR" {
		t.Fatalf("got %v", resp.GetFilters())
	}
}

func TestGetPortfolioFilters_EmptyPortfolioId(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolioFilters(ctx, &apiv1.GetPortfolioFiltersRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestGetPortfolioFilters_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetPortfolioFilters(ctx, &apiv1.GetPortfolioFiltersRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestSetPortfolioFilters_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		SetPortfolioFilters(gomock.Any(), "port-1", []dbpkg.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}}).
		Return(nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.SetPortfolioFilters(ctx, &apiv1.SetPortfolioFiltersRequest{
		PortfolioId: "port-1",
		Filters:     []*apiv1.PortfolioFilterProto{{FilterType: "broker", FilterValue: "IBKR"}},
	})
	if err != nil {
		t.Fatalf("SetPortfolioFilters: %v", err)
	}
}

func TestSetPortfolioFilters_EmptyPortfolioId(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.SetPortfolioFilters(ctx, &apiv1.SetPortfolioFiltersRequest{Filters: []*apiv1.PortfolioFilterProto{{FilterType: "broker", FilterValue: "IBKR"}}})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestSetPortfolioFilters_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.SetPortfolioFilters(ctx, &apiv1.SetPortfolioFiltersRequest{PortfolioId: "port-1", Filters: nil})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}
