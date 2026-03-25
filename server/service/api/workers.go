package api

import (
	"context"
	"sort"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/worker"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func workerStateToProto(s worker.State) apiv1.WorkerState {
	switch s {
	case worker.Idle:
		return apiv1.WorkerState_WORKER_STATE_IDLE
	case worker.Running:
		return apiv1.WorkerState_WORKER_STATE_RUNNING
	default:
		return apiv1.WorkerState_WORKER_STATE_UNSPECIFIED
	}
}

func (s *Server) ListWorkers(ctx context.Context, _ *apiv1.ListWorkersRequest) (*apiv1.ListWorkersResponse, error) {
	if _, err := auth.RequireAdmin(ctx); err != nil {
		return nil, err
	}
	if s.workerRegistry == nil {
		return &apiv1.ListWorkersResponse{}, nil
	}
	statuses := s.workerRegistry.List()
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Name < statuses[j].Name
	})
	workers := make([]*apiv1.Worker, len(statuses))
	for i, st := range statuses {
		workers[i] = &apiv1.Worker{
			Name:       st.Name,
			State:      workerStateToProto(st.State),
			Summary:    st.Summary,
			QueueDepth: int32(st.QueueDepth),
			UpdatedAt:  timestamppb.New(st.UpdatedAt),
		}
	}
	return &apiv1.ListWorkersResponse{Workers: workers}, nil
}
