package api

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func authCtx(userID, authSub string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub})
}

func authCtxWithProfile(userID, authSub, name, email string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, AuthSub: authSub, Name: name, Email: email})
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

// exportStreamMock provides a stream with configurable context for ExportInstruments tests.
type exportStreamMock struct {
	ctx  context.Context
	sent []*apiv1.Instrument
}

func (e *exportStreamMock) Context() context.Context    { return e.ctx }
func (e *exportStreamMock) RecvMsg(m interface{}) error { return nil }
func (e *exportStreamMock) Send(m *apiv1.Instrument) error {
	e.sent = append(e.sent, m)
	return nil
}
func (e *exportStreamMock) SendHeader(m metadata.MD) error { return nil }
func (e *exportStreamMock) SetHeader(m metadata.MD) error { return nil }
func (e *exportStreamMock) SetTrailer(m metadata.MD)       {}
func (e *exportStreamMock) SendMsg(m interface{}) error {
	if inst, ok := m.(*apiv1.Instrument); ok {
		e.sent = append(e.sent, inst)
	}
	return nil
}

// exportPriceStreamMock provides a stream with configurable context for ExportPrices tests.
type exportPriceStreamMock struct {
	ctx  context.Context
	sent []*apiv1.ExportPriceRow
}

func (e *exportPriceStreamMock) Context() context.Context    { return e.ctx }
func (e *exportPriceStreamMock) RecvMsg(m interface{}) error  { return nil }
func (e *exportPriceStreamMock) Send(m *apiv1.ExportPriceRow) error {
	e.sent = append(e.sent, m)
	return nil
}
func (e *exportPriceStreamMock) SendHeader(m metadata.MD) error { return nil }
func (e *exportPriceStreamMock) SetHeader(m metadata.MD) error  { return nil }
func (e *exportPriceStreamMock) SetTrailer(m metadata.MD)       {}
func (e *exportPriceStreamMock) SendMsg(m interface{}) error {
	if row, ok := m.(*apiv1.ExportPriceRow); ok {
		e.sent = append(e.sent, row)
	}
	return nil
}

func TestAPI_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"ListPortfolios", func() error { _, err := srv.ListPortfolios(ctx, &apiv1.ListPortfoliosRequest{}); return err }},
		{"GetPortfolio", func() error { _, err := srv.GetPortfolio(ctx, &apiv1.GetPortfolioRequest{PortfolioId: "any"}); return err }},
		{"CreatePortfolio", func() error { _, err := srv.CreatePortfolio(ctx, &apiv1.CreatePortfolioRequest{Name: "x"}); return err }},
		{"UpdatePortfolio", func() error { _, err := srv.UpdatePortfolio(ctx, &apiv1.UpdatePortfolioRequest{PortfolioId: "p", Name: "x"}); return err }},
		{"DeletePortfolio", func() error { _, err := srv.DeletePortfolio(ctx, &apiv1.DeletePortfolioRequest{PortfolioId: "p"}); return err }},
		{"ListTxs", func() error { _, err := srv.ListTxs(ctx, &apiv1.ListTxsRequest{}); return err }},
		{"GetHoldings", func() error { _, err := srv.GetHoldings(ctx, &apiv1.GetHoldingsRequest{}); return err }},
		{"GetPortfolioFilters", func() error { _, err := srv.GetPortfolioFilters(ctx, &apiv1.GetPortfolioFiltersRequest{PortfolioId: "p"}); return err }},
		{"SetPortfolioFilters", func() error { _, err := srv.SetPortfolioFilters(ctx, &apiv1.SetPortfolioFiltersRequest{PortfolioId: "p"}); return err }},
		{"GetJob", func() error { _, err := srv.GetJob(ctx, &apiv1.GetJobRequest{JobId: "job-1"}); return err }},
		{"ExportInstruments", func() error {
			stream := &exportStreamMock{ctx: context.Background()}
			return srv.ExportInstruments(&apiv1.ExportInstrumentsRequest{}, stream)
		}},
		{"ImportInstruments", func() error { _, err := srv.ImportInstruments(ctx, &apiv1.ImportInstrumentsRequest{}); return err }},
		{"ListInstruments", func() error { _, err := srv.ListInstruments(ctx, &apiv1.ListInstrumentsRequest{}); return err }},
		{"ListJobs", func() error { _, err := srv.ListJobs(ctx, &apiv1.ListJobsRequest{}); return err }},
		{"ListPrices", func() error { _, err := srv.ListPrices(ctx, &apiv1.ListPricesRequest{}); return err }},
		{"ExportPrices", func() error {
			stream := &exportPriceStreamMock{ctx: context.Background()}
			return srv.ExportPrices(&apiv1.ExportPricesRequest{}, stream)
		}},
		{"ImportPrices", func() error { _, err := srv.ImportPrices(ctx, &apiv1.ImportPricesRequest{}); return err }},
		{"GetPortfolioValuation", func() error {
			_, err := srv.GetPortfolioValuation(ctx, &apiv1.GetPortfolioValuationRequest{PortfolioId: "p", DateFrom: "2025-01-01", DateTo: "2025-01-03"})
			return err
		}},
		{"ListHoldingDeclarations", func() error { _, err := srv.ListHoldingDeclarations(ctx, &apiv1.ListHoldingDeclarationsRequest{}); return err }},
		{"CreateHoldingDeclaration", func() error {
			_, err := srv.CreateHoldingDeclaration(ctx, &apiv1.CreateHoldingDeclarationRequest{Broker: "IBKR", InstrumentId: "i", DeclaredQty: "1", AsOfDate: "2025-01-01"})
			return err
		}},
		{"UpdateHoldingDeclaration", func() error {
			_, err := srv.UpdateHoldingDeclaration(ctx, &apiv1.UpdateHoldingDeclarationRequest{Id: "d", DeclaredQty: "1", AsOfDate: "2025-01-01"})
			return err
		}},
		{"DeleteHoldingDeclaration", func() error { _, err := srv.DeleteHoldingDeclaration(ctx, &apiv1.DeleteHoldingDeclarationRequest{Id: "d"}); return err }},
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
