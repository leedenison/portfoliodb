package api

import (
	"time"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/worker"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/redis/go-redis/v9"
)

// JobEnqueuer enqueues a job for async processing. Returns an error if the queue is full.
type JobEnqueuer func(jobID, jobType string) error

// Server implements ApiService.
type Server struct {
	apiv1.UnimplementedApiServiceServer
	db             db.DB
	rdb            *redis.Client
	counterPrefix  string
	pluginRegistry *identifier.Registry
	descRegistry   *description.Registry
	priceRegistry  *pricefetcher.Registry
	priceTrigger   chan<- struct{}
	workerRegistry *worker.Registry
	enqueueJob     JobEnqueuer
}

// ServerConfig configures the API server.
type ServerConfig struct {
	DB             db.DB
	Redis          *redis.Client
	CounterPrefix  string
	PluginRegistry *identifier.Registry     // optional; enables display_name in identifier plugin list
	DescRegistry   *description.Registry    // optional; enables display_name in description plugin list
	PriceRegistry  *pricefetcher.Registry   // optional; enables display_name in price plugin list
	PriceTrigger   chan<- struct{}           // optional; when set, TriggerPriceFetch sends on it
	WorkerRegistry *worker.Registry         // optional; when set, ListWorkers returns worker status
	EnqueueJob     JobEnqueuer              // optional; when set, ImportPrices enqueues async jobs
}

// NewServer returns a new API server.
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		db:             cfg.DB,
		rdb:            cfg.Redis,
		counterPrefix:  cfg.CounterPrefix,
		pluginRegistry: cfg.PluginRegistry,
		descRegistry:   cfg.DescRegistry,
		priceRegistry:  cfg.PriceRegistry,
		priceTrigger:   cfg.PriceTrigger,
		workerRegistry: cfg.WorkerRegistry,
		enqueueJob:     cfg.EnqueueJob,
	}
}

// identifierTypeFromString maps DB identifier_type string to proto enum; returns UNSPECIFIED for unknown.
func identifierTypeFromString(s string) apiv1.IdentifierType {
	if v, ok := apiv1.IdentifierType_value[s]; ok {
		return apiv1.IdentifierType(v)
	}
	return apiv1.IdentifierType_IDENTIFIER_TYPE_UNSPECIFIED
}

func instrumentRowToProto(row *db.InstrumentRow) *apiv1.Instrument {
	if row == nil {
		return nil
	}
	identifiers := make([]*apiv1.InstrumentIdentifier, 0, len(row.Identifiers))
	for _, idn := range row.Identifiers {
		identifiers = append(identifiers, &apiv1.InstrumentIdentifier{Type: identifierTypeFromString(idn.Type), Domain: idn.Domain, Value: idn.Value, Canonical: idn.Canonical})
	}
	out := &apiv1.Instrument{
		Id:          row.ID,
		Identifiers: identifiers,
	}
	if row.AssetClass != nil {
		out.AssetClass = *row.AssetClass
	}
	if row.ExchangeMIC != nil {
		out.Exchange = *row.ExchangeMIC
	}
	if row.ExchangeName != nil || row.ExchangeAcronym != nil || row.ExchangeCountryCode != nil {
		out.ExchangeInfo = &apiv1.Exchange{
			Mic:         derefStr(row.ExchangeMIC),
			Name:        derefStr(row.ExchangeName),
			Acronym:     derefStr(row.ExchangeAcronym),
			CountryCode: derefStr(row.ExchangeCountryCode),
		}
	}
	if row.Currency != nil {
		out.Currency = *row.Currency
	}
	if row.Name != nil {
		out.Name = *row.Name
	}
	if row.UnderlyingID != nil {
		out.UnderlyingId = *row.UnderlyingID
	}
	if row.ValidFrom != nil {
		out.ValidFrom = timestamppb.New(*row.ValidFrom)
	}
	if row.ValidTo != nil {
		out.ValidTo = timestamppb.New(*row.ValidTo)
	}
	if row.CIK != nil {
		out.Cik = *row.CIK
	}
	if row.SICCode != nil {
		out.SicCode = *row.SICCode
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

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
