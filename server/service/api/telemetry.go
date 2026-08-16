package api

import (
	"context"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
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

// PurgeTelemetry deletes runs past the retention window, and with them the event
// rows beneath them (admin only).
//
// It does the delete rather than poking a worker, because there is no worker: the
// service does not schedule retention, and an external scheduler calls this. The
// count it returns is what that scheduler logs.
func (s *Server) PurgeTelemetry(ctx context.Context, req *apiv1.PurgeTelemetryRequest) (*apiv1.PurgeTelemetryResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if s.telemetryDB == nil {
		return nil, status.Error(codes.Unavailable, "telemetry is not configured")
	}
	deleted, err := s.telemetryDB.PurgeRunsBefore(ctx, time.Now().Add(-db.TelemetryRetention))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.PurgeTelemetryResponse{RunsDeleted: deleted}, nil
}
