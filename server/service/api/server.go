package api

import (
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/redis/go-redis/v9"
)

// Server implements ApiService.
type Server struct {
	apiv1.UnimplementedApiServiceServer
	db             db.DB
	rdb            *redis.Client
	counterPrefix  string
}

// NewServer returns a new API server. rdb and counterPrefix are used for ListTelemetryCounters (admin).
func NewServer(database db.DB, rdb *redis.Client, counterPrefix string) *Server {
	return &Server{db: database, rdb: rdb, counterPrefix: counterPrefix}
}

func instrumentRowToProto(row *db.InstrumentRow) *apiv1.Instrument {
	if row == nil {
		return nil
	}
	identifiers := make([]*apiv1.InstrumentIdentifier, 0, len(row.Identifiers))
	for _, idn := range row.Identifiers {
		identifiers = append(identifiers, &apiv1.InstrumentIdentifier{Type: idn.Type, Domain: idn.Domain, Value: idn.Value, Canonical: idn.Canonical})
	}
	out := &apiv1.Instrument{
		Id:           row.ID,
		AssetClass:   row.AssetClass,
		Exchange:     row.Exchange,
		Currency:     row.Currency,
		Name:         row.Name,
		Identifiers:  identifiers,
		UnderlyingId: row.UnderlyingID,
	}
	if row.ValidFrom != nil {
		out.ValidFrom = timestamppb.New(*row.ValidFrom)
	}
	if row.ValidTo != nil {
		out.ValidTo = timestamppb.New(*row.ValidTo)
	}
	return out
}

// protoValidFrom converts optional proto timestamp to *time.Time for DB.
func protoValidFrom(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || !ts.IsValid() {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// protoValidTo converts optional proto timestamp to *time.Time for DB.
func protoValidTo(ts *timestamppb.Timestamp) *time.Time {
	return protoValidFrom(ts)
}
