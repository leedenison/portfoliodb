package api

import (
	"context"
	"reflect"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
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

// streamCalls supplies the one thing reflection cannot: a server stream to write
// into, which is a different type per method and has no zero value worth calling.
//
// The loop below fails naming any streaming method missing from here, so this stays
// as short as the service is and cannot silently fall behind it.
var streamCalls = map[string]func(*Server, context.Context) error{
	"ExportSystemArchive": func(s *Server, ctx context.Context) error {
		return s.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{}, &exportArchiveStreamMock{ctx: ctx})
	},
	"ExportUserArchive": func(s *Server, ctx context.Context) error {
		return s.ExportUserArchive(&apiv1.ExportUserArchiveRequest{}, &exportUserStreamMock{ctx: ctx})
	},
}

// Every method the service declares refuses a caller who is not signed in.
//
// Driven off the generated service descriptor rather than a list kept here by hand.
// A list is only as good as whoever remembered to add to it, and what that missed was
// not random: the untested RPCs were the structural copies -- the identifier,
// description, inflation and corporate-event plugin methods beside the price ones
// that were listed -- so the guarantee held for the half somebody had thought about.
// Reading the descriptor means a method added to the proto is covered the moment it
// is generated, and one whose guard is dropped fails here rather than in review.
//
// The request is the zero value of whatever the method takes. That is enough because
// the guard is the first thing every method does: none of them validates a field,
// reads the store or reaches a registry before it. A method that stopped doing that
// would fail here with InvalidArgument rather than pass quietly, which is the point
// -- the order is the property, and this is what holds it.
//
// The mock store carries no expectations, so a guard that let a call through would
// fail the test on the unexpected call as well as on the code.
func TestAPI_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	for _, m := range apiv1.ApiService_ServiceDesc.Methods {
		t.Run(m.MethodName, func(t *testing.T) {
			testutil.RequireGRPCCode(t, callUnary(t, srv, m.MethodName), codes.Unauthenticated)
		})
	}
	for _, s := range apiv1.ApiService_ServiceDesc.Streams {
		call, ok := streamCalls[s.StreamName]
		if !ok {
			t.Errorf("%s: streaming method with no entry in streamCalls, so nothing checks its guard", s.StreamName)
			continue
		}
		t.Run(s.StreamName, func(t *testing.T) {
			testutil.RequireGRPCCode(t, call(srv, context.Background()), codes.Unauthenticated)
		})
	}
}

// callUnary invokes one unary method with an empty request and no user in the
// context, and returns the error it gave.
func callUnary(t *testing.T, srv *Server, name string) error {
	t.Helper()
	m := reflect.ValueOf(srv).MethodByName(name)
	if !m.IsValid() {
		t.Fatalf("%s: the descriptor declares it but *Server has no such method", name)
	}
	typ := m.Type()
	if typ.NumIn() != 2 || typ.NumOut() != 2 {
		t.Fatalf("%s: not the shape of a unary handler: %s", name, typ)
	}
	out := m.Call([]reflect.Value{
		reflect.ValueOf(context.Background()),
		reflect.New(typ.In(1).Elem()),
	})
	err, _ := out[1].Interface().(error)
	return err
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
