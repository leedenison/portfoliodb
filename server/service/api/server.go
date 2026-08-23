package api

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/corporateevents"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/candidate"
	"github.com/leedenison/portfoliodb/server/inflationfetcher"
	"github.com/leedenison/portfoliodb/server/pricefetcher"
	"github.com/leedenison/portfoliodb/server/worker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// JobEnqueuer enqueues a job for async processing. Returns an error if the queue is full.
type JobEnqueuer func(jobID, jobType string) error

// Server implements ApiService.
type Server struct {
	apiv1.UnimplementedApiServiceServer
	db                     db.DB
	telemetryDB            db.TelemetryDB
	pluginRegistry         *identifier.Registry
	candRegistry           *candidate.Registry
	priceRegistry          *pricefetcher.Registry
	priceTrigger           chan<- struct{}
	inflationRegistry      *inflationfetcher.Registry
	inflationTrigger       chan<- struct{}
	corporateEventRegistry *corporateevents.Registry
	corporateEventTrigger  chan<- struct{}
	transferMatchTrigger   chan<- struct{}
	groupingTrigger        chan<- struct{}
	workerRegistry         *worker.Registry
	enqueueJob             JobEnqueuer
}

// ServerConfig configures the API server.
type ServerConfig struct {
	DB                     db.DB
	TelemetryDB            db.TelemetryDB             // optional; when set, PurgeTelemetry deletes past the retention window
	PluginRegistry         *identifier.Registry       // optional; enables display_name in identifier plugin list
	CandidateRegistry      *candidate.Registry        // optional; enables display_name in candidate plugin list
	PriceRegistry          *pricefetcher.Registry     // optional; enables display_name in price plugin list
	PriceTrigger           chan<- struct{}            // optional; when set, TriggerPriceFetch sends on it
	InflationRegistry      *inflationfetcher.Registry // optional; enables display_name in inflation plugin list
	InflationTrigger       chan<- struct{}            // optional; when set, TriggerInflationFetch sends on it
	CorporateEventRegistry *corporateevents.Registry  // optional; enables display_name in corporate event plugin list
	CorporateEventTrigger  chan<- struct{}            // optional; when set, TriggerCorporateEventFetch sends on it
	TransferMatchTrigger   chan<- struct{}            // optional; when set, TriggerTransferMatch sends on it
	GroupingTrigger        chan<- struct{}            // optional; when set, TriggerGrouping sends on it
	WorkerRegistry         *worker.Registry           // optional; when set, ListWorkers returns worker status
	EnqueueJob             JobEnqueuer                // optional; when set, ImportPrices enqueues async jobs
}

// NewServer returns a new API server.
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		db:                     cfg.DB,
		telemetryDB:            cfg.TelemetryDB,
		pluginRegistry:         cfg.PluginRegistry,
		candRegistry:           cfg.CandidateRegistry,
		priceRegistry:          cfg.PriceRegistry,
		priceTrigger:           cfg.PriceTrigger,
		inflationRegistry:      cfg.InflationRegistry,
		inflationTrigger:       cfg.InflationTrigger,
		corporateEventRegistry: cfg.CorporateEventRegistry,
		corporateEventTrigger:  cfg.CorporateEventTrigger,
		transferMatchTrigger:   cfg.TransferMatchTrigger,
		groupingTrigger:        cfg.GroupingTrigger,
		workerRegistry:         cfg.WorkerRegistry,
		enqueueJob:             cfg.EnqueueJob,
	}
}

// identifierTypeFromString maps DB identifier_type string to proto enum; returns UNSPECIFIED for unknown.
func identifierTypeFromString(s string) typev1.IdentifierType {
	if v, ok := typev1.IdentifierType_value[s]; ok {
		return typev1.IdentifierType(v)
	}
	return typev1.IdentifierType_IDENTIFIER_TYPE_UNSPECIFIED
}

func instrumentRowToProto(row *db.InstrumentRow) *apiv1.Instrument {
	if row == nil {
		return nil
	}
	// Flattened, because the UI reads one list per instrument and the identity
	// page it draws does not yet say which grain a row is at. Each listing also
	// carries its own below, so the two views are consistent and a caller can
	// already read the grain off the response. Removing the flat one is 0154.
	identifiers := identifiersToProto(row.AllIdentifiers())
	out := &apiv1.Instrument{
		Id:          row.ID,
		Identifiers: identifiers,
	}
	if row.AssetClass != nil {
		out.AssetClass = db.StrToAssetClass(*row.AssetClass)
	}
	out.Exchange = derefStr(row.ExchangeMIC)
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
	if row.UnderlyingListingID != nil {
		out.UnderlyingListingId = *row.UnderlyingListingID
	}
	if row.UnderlyingID != nil {
		out.UnderlyingId = *row.UnderlyingID
	}
	if row.ValidFrom != nil {
		out.ValidFrom = timestamppb.New(*row.ValidFrom)
	}
	if row.ValidBefore != nil {
		out.ValidBefore = timestamppb.New(*row.ValidBefore)
	}
	if row.CIK != nil {
		out.Cik = *row.CIK
	}
	if row.SICCode != nil {
		out.SicCode = *row.SICCode
	}
	out.Strike = decStrPtr(row.Strike)
	if row.Expiry != nil {
		out.Expiry = row.Expiry.Format("2006-01-02")
	}
	if row.PutCall != nil {
		out.PutCall = *row.PutCall
	}
	out.ContractMultiplier = decStrPtr(&row.ContractMultiplier)
	out.Listings = listingsToProto(row.Listings)
	return out
}

// identifiersToProto converts identifier rows at either grain. The row carries
// nothing that says which, and nothing here needs to: what differs between the
// two is where they are stored and what they name, not how they are rendered.
func identifiersToProto(idns []db.IdentifierInput) []*apiv1.InstrumentIdentifier {
	out := make([]*apiv1.InstrumentIdentifier, 0, len(idns))
	for _, idn := range idns {
		pi := &apiv1.InstrumentIdentifier{Type: identifierTypeFromString(idn.Ref.Type), Domain: idn.Ref.Domain, Value: idn.Ref.Value, Canonical: idn.Canonical}
		if idn.ValidFrom != nil {
			pi.ValidFrom = proto.String(idn.ValidFrom.Format("2006-01-02"))
		}
		if idn.ValidBefore != nil {
			pi.ValidBefore = proto.String(idn.ValidBefore.Format("2006-01-02"))
		}
		out = append(out, pi)
	}
	return out
}

// listingsToProto converts a security's currency lines. A nil Currency is the
// unknown listing and becomes an empty string, as every other nullable string on
// this message does.
func listingsToProto(listings []*db.Listing) []*apiv1.Listing {
	if len(listings) == 0 {
		return nil
	}
	out := make([]*apiv1.Listing, 0, len(listings))
	for _, l := range listings {
		pl := &apiv1.Listing{Id: l.ID, Currency: derefStr(l.Currency), Venues: l.Venues}
		if len(l.Identifiers) > 0 {
			pl.Identifiers = identifiersToProto(l.Identifiers)
		}
		if l.ValidFrom != nil {
			pl.ValidFrom = proto.String(l.ValidFrom.Format("2006-01-02"))
		}
		if l.ValidBefore != nil {
			pl.ValidBefore = proto.String(l.ValidBefore.Format("2006-01-02"))
		}
		out = append(out, pl)
	}
	return out
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
