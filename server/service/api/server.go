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
	// At their grain rather than flattened: the security's own names here, each
	// line's on the listing it names, and the ones nobody could place in a third
	// list. A reader wanting every name a security answers to reads all three,
	// which is what a reader that has picked a grain no longer has to do.
	out := &apiv1.Instrument{
		Id:                  row.ID,
		Identifiers:         identifiersToProto(row.Identifiers),
		UnplacedIdentifiers: identifiersToProto(row.UnplacedIdentifiers),
	}
	if row.AssetClass != nil {
		out.AssetClass = db.StrToAssetClass(*row.AssetClass)
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

// listingsToProto converts a security's currency lines.
func listingsToProto(listings []*db.Listing) []*apiv1.Listing {
	if len(listings) == 0 {
		return nil
	}
	out := make([]*apiv1.Listing, 0, len(listings))
	for _, l := range listings {
		pl := &apiv1.Listing{Id: l.ID, Currency: l.Currency, Venues: venuesToProto(l.Venues)}
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

// venuesToProto converts a line's venues, each with the reference data joined to
// its MIC.
//
// It is where the exchange reference data reaches the wire, and it reaches it per
// line: a security is admitted to no venue, its lines are, so there is nothing
// above the line for this to hang off.
func venuesToProto(venues []db.Venue) []*apiv1.Exchange {
	if len(venues) == 0 {
		return nil
	}
	out := make([]*apiv1.Exchange, 0, len(venues))
	for _, v := range venues {
		out = append(out, &apiv1.Exchange{
			Mic:         v.MIC,
			Name:        v.Name,
			Acronym:     v.Acronym,
			CountryCode: v.CountryCode,
		})
	}
	return out
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
