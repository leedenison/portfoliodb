package api

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
)

func TestGetHoldings_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", SplitAdjustedQuantity: "10"}}
	asOf := timestamppb.Now()
	db.EXPECT().
		ComputeHoldings(gomock.Any(), "user-1", (*typev1.Broker)(nil), "", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{})
	if err != nil {
		t.Fatalf("GetHoldings: %v", err)
	}
	if len(resp.GetHoldings()) != 1 || resp.GetHoldings()[0].GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetHoldings())
	}
	if resp.GetAsOf() == nil {
		t.Fatal("asOf should be set")
	}
}

func TestGetHoldings_WithPortfolioId_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	holdings := []*apiv1.Holding{{InstrumentDescription: "AAPL", SplitAdjustedQuantity: "10"}}
	asOf := timestamppb.Now()
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(true, nil)
	db.EXPECT().
		ComputeHoldingsForPortfolio(gomock.Any(), "port-1", (*timestamppb.Timestamp)(nil)).
		Return(holdings, asOf, nil)
	ctx := authCtx("user-1", "sub|1")
	resp, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "port-1"})
	if err != nil {
		t.Fatalf("GetHoldings: %v", err)
	}
	if len(resp.GetHoldings()) != 1 || resp.GetHoldings()[0].GetInstrumentDescription() != "AAPL" {
		t.Fatalf("got %v", resp.GetHoldings())
	}
}

func TestGetHoldings_WithPortfolioId_NotFound(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().
		PortfolioBelongsToUser(gomock.Any(), "port-1", "user-1").
		Return(false, nil)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{PortfolioId: "port-1"})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestCountUnattributedHoldings_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.CountUnattributedHoldings(authCtx("user-1", "sub|1"), &apiv1.CountUnattributedHoldingsRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestCountUnattributedHoldings_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.CountUnattributedHoldings(ctxNoAuth(), &apiv1.CountUnattributedHoldingsRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

// The two counts stay apart on the way out. They are different repairs -- one on
// the postings, one on the security -- and a surface that added them would be
// telling a person how many problems there are without saying where any of them
// is.
func TestCountUnattributedHoldings_Success(t *testing.T) {
	srv, db := newAPIServerWithMock(t)
	db.EXPECT().CountUnattributedHoldings(gomock.Any()).Return(int32(4), int32(2), nil)

	resp, err := srv.CountUnattributedHoldings(adminCtx("user-1", "sub|1"), &apiv1.CountUnattributedHoldingsRequest{})
	if err != nil {
		t.Fatalf("CountUnattributedHoldings: %v", err)
	}
	if resp.GetNoLineNamedCount() != 4 {
		t.Errorf("no line named = %d, want 4", resp.GetNoLineNamedCount())
	}
	if resp.GetNoCurrencyKnownCount() != 2 {
		t.Errorf("no currency known = %d, want 2", resp.GetNoCurrencyKnownCount())
	}
}
