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
