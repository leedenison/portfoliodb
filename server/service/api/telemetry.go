package api

import (
	"context"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// telemetry is the telemetry reader, or one that answers with nothing.
//
// A serving path reads telemetry only to show a person what happened, so a
// deployment without a telemetry pool shows an empty list rather than an error:
// there is nothing the system needs from it, which is the whole point of the
// rule that no functional path depends on this schema.
func (s *Server) telemetry() db.TelemetryDB {
	if s.telemetryDB == nil {
		return db.NopTelemetry{}
	}
	return s.telemetryDB
}
