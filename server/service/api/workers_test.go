package api

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/leedenison/portfoliodb/server/worker"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
)

func TestListWorkers_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListWorkers(ctxNoAuth(), &apiv1.ListWorkersRequest{})
	testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
}

func TestListWorkers_NonAdmin(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListWorkers(authCtx("user-1", "sub|1"), &apiv1.ListWorkersRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestListWorkers_NilRegistry(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	resp, err := srv.ListWorkers(adminCtx("admin-1", "sub|admin"), &apiv1.ListWorkersRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Workers) != 0 {
		t.Fatalf("expected 0 workers, got %d", len(resp.Workers))
	}
}

func TestListWorkers_ReturnsStatus(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	reg := worker.NewRegistry()
	srv.workerRegistry = reg

	reg.SetIdle("ingestion")
	reg.SetRunning("price_fetcher", "Fetching prices for 5 instruments")
	reg.SetQueueDepth("ingestion", 3)

	resp, err := srv.ListWorkers(adminCtx("admin-1", "sub|admin"), &apiv1.ListWorkersRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(resp.Workers))
	}
	// Sorted by name: ingestion, price_fetcher.
	ing := resp.Workers[0]
	if ing.Name != "ingestion" || ing.State != apiv1.WorkerState_WORKER_STATE_IDLE || ing.QueueDepth != 3 {
		t.Errorf("ingestion: got name=%q state=%v queue=%d", ing.Name, ing.State, ing.QueueDepth)
	}
	pf := resp.Workers[1]
	if pf.Name != "price_fetcher" || pf.State != apiv1.WorkerState_WORKER_STATE_RUNNING || pf.Summary != "Fetching prices for 5 instruments" {
		t.Errorf("price_fetcher: got name=%q state=%v summary=%q", pf.Name, pf.State, pf.Summary)
	}
}
