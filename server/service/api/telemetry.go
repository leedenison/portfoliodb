package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListTelemetryCounters returns all counters discovered from Redis (admin only).
func (s *Server) ListTelemetryCounters(ctx context.Context, req *apiv1.ListTelemetryCountersRequest) (*apiv1.ListTelemetryCountersResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	entries, err := telemetry.ListCounters(ctx, s.rdb, s.counterPrefix)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	counters := make([]*apiv1.TelemetryCounter, 0, len(entries))
	for _, e := range entries {
		counters = append(counters, &apiv1.TelemetryCounter{Name: e.Name, Value: e.Value})
	}
	return &apiv1.ListTelemetryCountersResponse{Counters: counters}, nil
}
