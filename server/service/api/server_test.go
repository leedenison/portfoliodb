package api

import (
	"context"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
)

func ctxNoAuth() context.Context {
	return context.Background()
}

func authCtx(userID, authSub string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub})
}

// adminCtx returns a context with an admin user (for Export/Import RPCs).
func adminCtx(userID, authSub string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub, Role: "admin"})
}

// newAPIServerWithMock creates a gomock controller, mock DB, and API server. The controller is finished when the test ends.
func newAPIServerWithMock(t *testing.T) (*Server, *mock.MockDB) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	db := mock.NewMockDB(ctrl)
	return NewServer(ServerConfig{DB: db}), db
}

func TestAPI_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"ListPortfolios", func() error { _, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{}); return err }},
		{"GetPortfolio", func() error {
			_, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "any"})
			return err
		}},
		{"CreatePortfolio", func() error { _, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "x"}); return err }},
		{"UpdatePortfolio", func() error {
			_, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "p", Name: "x"})
			return err
		}},
		{"DeletePortfolio", func() error {
			_, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "p"})
			return err
		}},
		{"ListTxs", func() error { _, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{}); return err }},
		{"GetHoldings", func() error { _, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{}); return err }},
		{"GetPortfolioFilters", func() error {
			_, err := srv.GetPortfolioFilters(ctx, &apiv1.GetPortfolioFiltersRequest{PortfolioId: "p"})
			return err
		}},
		{"SetPortfolioFilters", func() error {
			_, err := srv.SetPortfolioFilters(ctx, &apiv1.SetPortfolioFiltersRequest{PortfolioId: "p"})
			return err
		}},
		{"GetJob", func() error { _, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"}); return err }},
		{"ExportSystemArchive", func() error {
			stream := &exportArchiveStreamMock{ctx: context.Background()}
			return srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{}, stream)
		}},
		{"ImportSystemArchive", func() error {
			_, err := srv.ImportSystemArchive(ctx, &apiv1.ImportSystemArchiveRequest{})
			return err
		}},
		{"ListInstruments", func() error { _, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{}); return err }},
		{"ListJobs", func() error { _, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{}); return err }},
		{"ListPrices", func() error { _, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{}); return err }},
		{"GetPortfolioValuation", func() error {
			_, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{PortfolioId: "p", DateFrom: &date.Date{Year: 2025, Month: 1, Day: 1}, DateBefore: &date.Date{Year: 2025, Month: 1, Day: 3}})
			return err
		}},
		{"ListPriceGaps", func() error { _, err := srv.ListPriceGaps(ctx, &apiv1.ListPriceGapsRequest{}); return err }},
		{"ListHoldingDeclarations", func() error {
			_, err := srv.ListHoldingDeclarations(ctx, &apiv1.ListHoldingDeclarationsRequest{})
			return err
		}},
		{"CreateHoldingDeclaration", func() error {
			_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{Broker: "IBKR", InstrumentId: "i", DeclaredQty: "1", AsOfDate: &date.Date{Year: 2025, Month: 1, Day: 1}})
			return err
		}},
		{"UpdateHoldingDeclaration", func() error {
			_, err := srv.UpdateHoldingDeclaration(ctx, &apiv1.UpdateHoldingDeclarationRequest{Id: "d", DeclaredQty: "1", AsOfDate: &date.Date{Year: 2025, Month: 1, Day: 1}})
			return err
		}},
		{"DeleteHoldingDeclaration", func() error {
			_, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{Id: "d"})
			return err
		}},
		{"GetDisplayCurrency", func() error { _, err := srv.GetDisplayCurrency(ctx, &apiv1.GetDisplayCurrencyRequest{}); return err }},
		{"SetDisplayCurrency", func() error {
			_, err := srv.SetDisplayCurrency(ctx, &apiv1.SetDisplayCurrencyRequest{DisplayCurrency: "EUR"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
		})
	}
}

func TestAPI_InvalidArgument(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	tests := []struct {
		name string
		call func() error
	}{
		{"GetPortfolio_empty_portfolio_id", func() error { _, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{}); return err }},
		{"CreatePortfolio_empty_name", func() error { _, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{}); return err }},
		{"UpdatePortfolio_empty_portfolio_id", func() error { _, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{Name: "x"}); return err }},
		{"DeletePortfolio_empty_portfolio_id", func() error { _, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{}); return err }},
		{"GetJob_empty_job_id", func() error { _, err := srv.GetJob(ctx, &apiv1.GetJobRequest{}); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}
