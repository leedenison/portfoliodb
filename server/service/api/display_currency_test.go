package api

import (
	"fmt"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestGetDisplayCurrency_Success(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("EUR", nil)

	resp, err := srv.GetDisplayCurrency(ctx, &apiv1.GetDisplayCurrencyRequest{})
	if err != nil {
		t.Fatalf("GetDisplayCurrency: %v", err)
	}
	if resp.DisplayCurrency != "EUR" {
		t.Fatalf("want EUR, got %q", resp.DisplayCurrency)
	}
}

func TestGetDisplayCurrency_DBError(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		GetDisplayCurrency(gomock.Any(), "user-1").
		Return("", fmt.Errorf("db boom"))

	_, err := srv.GetDisplayCurrency(ctx, &apiv1.GetDisplayCurrencyRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestSetDisplayCurrency_Success(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	mdb.EXPECT().
		SetDisplayCurrency(gomock.Any(), "user-1", "GBP").
		Return(nil)

	resp, err := srv.SetDisplayCurrency(ctx, &apiv1.SetDisplayCurrencyRequest{DisplayCurrency: "GBP"})
	if err != nil {
		t.Fatalf("SetDisplayCurrency: %v", err)
	}
	if resp.DisplayCurrency != "GBP" {
		t.Fatalf("want GBP, got %q", resp.DisplayCurrency)
	}
}

func TestSetDisplayCurrency_InvalidCode(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")

	tests := []struct {
		name string
		code string
	}{
		{"empty", ""},
		{"lowercase", "usd"},
		{"too_short", "US"},
		{"too_long", "USDD"},
		{"has_digits", "US1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.SetDisplayCurrency(ctx, &apiv1.SetDisplayCurrencyRequest{DisplayCurrency: tc.code})
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}
