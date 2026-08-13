package api

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// The trigger RPCs nudge a worker to run a cycle now rather than at its cadence.
// Each is the same three properties: it sends when a channel is wired, it returns
// rather than panicking when one is not, and it never blocks -- a worker already
// running has a full buffer, and an admin asking twice must not hang the request.
//
// TriggerPriceFetch has these three tested in plugins_test.go. The inflation and
// corporate-event ones are the copies, and had none.
type triggerRPC struct {
	name string
	// server builds a server with this trigger wired to ch, and nothing else.
	server func(ch chan struct{}) *Server
	call   func(*Server, context.Context) error
}

var triggerRPCs = []triggerRPC{
	{
		name:   "TriggerInflationFetch",
		server: func(ch chan struct{}) *Server { return NewServer(ServerConfig{InflationTrigger: ch}) },
		call: func(s *Server, ctx context.Context) error {
			_, err := s.TriggerInflationFetch(ctx, &apiv1.TriggerInflationFetchRequest{})
			return err
		},
	},
	{
		name:   "TriggerCorporateEventFetch",
		server: func(ch chan struct{}) *Server { return NewServer(ServerConfig{CorporateEventTrigger: ch}) },
		call: func(s *Server, ctx context.Context) error {
			_, err := s.TriggerCorporateEventFetch(ctx, &apiv1.TriggerCorporateEventFetchRequest{})
			return err
		},
	},
}

func TestTriggers_Send(t *testing.T) {
	for _, tr := range triggerRPCs {
		t.Run(tr.name, func(t *testing.T) {
			ch := make(chan struct{}, 1)
			if err := tr.call(tr.server(ch), adminCtx("admin-1", "sub|admin")); err != nil {
				t.Fatalf("%s: %v", tr.name, err)
			}
			select {
			case <-ch:
			default:
				t.Errorf("%s: nothing was sent on the trigger", tr.name)
			}
		})
	}
}

// An instance running without that worker has no channel, and the RPC succeeds
// having done nothing rather than failing on a nil send.
func TestTriggers_NilChannel(t *testing.T) {
	for _, tr := range triggerRPCs {
		t.Run(tr.name, func(t *testing.T) {
			if err := tr.call(tr.server(nil), adminCtx("admin-1", "sub|admin")); err != nil {
				t.Errorf("%s with no trigger wired: %v", tr.name, err)
			}
		})
	}
}

// The buffer is already full, which is what a worker mid-cycle looks like. The send
// is dropped and the call returns, because the pending run will pick up whatever
// this one would have.
func TestTriggers_NonBlocking(t *testing.T) {
	for _, tr := range triggerRPCs {
		t.Run(tr.name, func(t *testing.T) {
			ch := make(chan struct{}, 1)
			ch <- struct{}{}
			done := make(chan error, 1)
			go func() { done <- tr.call(tr.server(ch), adminCtx("admin-1", "sub|admin")) }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("%s: %v", tr.name, err)
				}
			case <-time.After(time.Second):
				t.Errorf("%s blocked on a full trigger", tr.name)
			}
		})
	}
}

func TestTriggers_AdminOnly(t *testing.T) {
	for _, tr := range triggerRPCs {
		t.Run(tr.name, func(t *testing.T) {
			ch := make(chan struct{}, 1)
			err := tr.call(tr.server(ch), authCtx("user-1", "sub|1"))
			testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
			if len(ch) != 0 {
				t.Errorf("%s sent on the trigger despite refusing the caller", tr.name)
			}
		})
	}
}

// ListTelemetryCounters reads Redis, and an instance with none configured reports
// no counters rather than failing. That is the whole of what can be checked here:
// the scan and decode want a Redis to scan, which this package has no fake for.
func TestListTelemetryCounters_NoRedis(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	srv := NewServer(ServerConfig{DB: mock.NewMockDB(ctrl)})

	resp, err := srv.ListTelemetryCounters(adminCtx("admin-1", "sub|admin"), &apiv1.ListTelemetryCountersRequest{})
	if err != nil {
		t.Fatalf("ListTelemetryCounters: %v", err)
	}
	if len(resp.GetCounters()) != 0 {
		t.Errorf("counters: got %d, want none", len(resp.GetCounters()))
	}
}

func TestListTelemetryCounters_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListTelemetryCounters(authCtx("user-1", "sub|1"), &apiv1.ListTelemetryCountersRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}
