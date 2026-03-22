package api

import (
	"context"
	"regexp"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var currencyCodeRE = regexp.MustCompile(`^[A-Z]{3}$`)

// GetDisplayCurrency returns the authenticated user's display currency preference.
func (s *Server) GetDisplayCurrency(ctx context.Context, _ *apiv1.GetDisplayCurrencyRequest) (*apiv1.GetDisplayCurrencyResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	dc, err := s.db.GetDisplayCurrency(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.GetDisplayCurrencyResponse{DisplayCurrency: dc}, nil
}

// SetDisplayCurrency updates the authenticated user's display currency preference.
func (s *Server) SetDisplayCurrency(ctx context.Context, req *apiv1.SetDisplayCurrencyRequest) (*apiv1.SetDisplayCurrencyResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	cc := req.GetDisplayCurrency()
	if !currencyCodeRE.MatchString(cc) {
		return nil, status.Error(codes.InvalidArgument, "display_currency must be a 3-letter ISO 4217 code")
	}
	if err := s.db.SetDisplayCurrency(ctx, u.ID, cc); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Trigger a price fetch cycle so FX rates for the new display currency are fetched.
	pricefetcher.Trigger(s.priceTrigger)
	return &apiv1.SetDisplayCurrencyResponse{DisplayCurrency: cc}, nil
}
