package api

import (
	"context"
	"errors"
	"fmt"
	"sort"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ExportCorporateEvents streams the corporate event part of a system archive:
// the envelope first, then one group per instrument. Admin only.
//
// The coverage nested in each group is not derivable from its events: events are
// sparse, so a span holding none of them records that a provider was asked and
// had nothing, which is a different statement from never having asked.
func (s *Server) ExportCorporateEvents(req *apiv1.ExportCorporateEventsRequest, stream apiv1.ApiService_ExportCorporateEventsServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	coverage, err := s.db.ListCorporateEventCoverageForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	splits, err := s.db.ListStockSplitsForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	dividends, err := s.db.ListCashDividendsForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// source_instance is left empty: nothing keys off it, and this build has no
	// configured identity to put there.
	if err := stream.Send(&apiv1.ExportCorporateEventsResponse{
		Item: &apiv1.ExportCorporateEventsResponse_Envelope{
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_SYSTEM),
		},
	}); err != nil {
		return err
	}
	for _, g := range corporateEventGroups(splits, dividends, coverage) {
		if err := stream.Send(&apiv1.ExportCorporateEventsResponse{
			Item: &apiv1.ExportCorporateEventsResponse_Group{Group: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// corporateEventGroups turns the three flat export queries into archive groups,
// one per instrument.
//
// An instrument that was covered and has no events still gets a group, with
// empty events. It is the only way a file can say a provider was asked about
// those dates and had nothing, and the coverage row is where such a group's
// asset class comes from.
//
// All three inputs arrive ordered by identifier, so the events are grouped by a
// scan and the output order follows the queries'. The data provider is not
// carried: an import records every event and every span against the "import"
// sentinel, so provenance cannot survive a round trip.
func corporateEventGroups(splits []db.ExportStockSplit, dividends []db.ExportCashDividend, coverage []db.ExportCoverageRow) []*archivev1.CorporateEventGroup {
	var order []instKey
	groups := make(map[instKey]*archivev1.CorporateEventGroup)

	group := func(k instKey, assetClass string) *archivev1.CorporateEventGroup {
		g, ok := groups[k]
		if ok {
			return g
		}
		g = &archivev1.CorporateEventGroup{
			Instrument: &archivev1.InstrumentRef{
				Type:   identifierTypeFromString(k.typ),
				Value:  k.value,
				Domain: k.domain,
			},
			AssetClass: db.StrToAssetClass(assetClass),
		}
		groups[k] = g
		order = append(order, k)
		return g
	}

	for _, c := range coverage {
		k := instKey{c.IdentifierType, c.IdentifierValue, c.IdentifierDomain}
		g := group(k, c.AssetClass)
		g.Coverage = append(g.Coverage, &archivev1.DateInterval{
			From:   c.From.Format("2006-01-02"),
			Before: c.Before.Format("2006-01-02"),
		})
	}
	for _, r := range splits {
		k := instKey{r.IdentifierType, r.IdentifierValue, r.IdentifierDomain}
		g := group(k, r.AssetClass)
		g.Events = append(g.Events, &archivev1.CorporateEvent{
			Event: &archivev1.CorporateEvent_Split{Split: &archivev1.Split{
				ExDate:       r.ExDate.Format("2006-01-02"),
				SplitFrom:    r.SplitFrom,
				SplitTo:      r.SplitTo,
				FirstKnownAt: timestamppb.New(r.FirstKnownAt),
			}},
		})
	}
	for _, r := range dividends {
		k := instKey{r.IdentifierType, r.IdentifierValue, r.IdentifierDomain}
		g := group(k, r.AssetClass)
		g.Events = append(g.Events, &archivev1.CorporateEvent{
			Event: &archivev1.CorporateEvent_Dividend{Dividend: archiveDividend(r)},
		})
	}

	out := make([]*archivev1.CorporateEventGroup, 0, len(order))
	for _, k := range order {
		g := groups[k]
		// The queries order splits and dividends separately, so a group holding
		// both would read as one block then the other. Order in a file is a
		// convenience rather than a contract, but a group that reads
		// chronologically is the one a human can check against a statement.
		sort.SliceStable(g.Events, func(a, b int) bool {
			return eventExDate(g.Events[a]) < eventExDate(g.Events[b])
		})
		out = append(out, g)
	}
	return out
}

// eventExDate is the valid time of either arm of the event oneof.
func eventExDate(e *archivev1.CorporateEvent) string {
	if sp := e.GetSplit(); sp != nil {
		return sp.GetExDate()
	}
	return e.GetDividend().GetExDate()
}

// archiveDividend converts one cash dividend export row to its archive form.
func archiveDividend(r db.ExportCashDividend) *archivev1.CashDividend {
	d := &archivev1.CashDividend{
		ExDate:          r.ExDate.Format("2006-01-02"),
		PayDate:         optDate(r.PayDate),
		RecordDate:      optDate(r.RecordDate),
		DeclarationDate: optDate(r.DeclarationDate),
		Amount:          r.Amount,
		Currency:        r.Currency,
		Type:            dividendTypeFromString(r.Type),
		FirstKnownAt:    timestamppb.New(r.FirstKnownAt),
	}
	if r.Frequency != "" {
		d.Frequency = proto.String(r.Frequency)
	}
	return d
}

// dividendTypeFromString maps the stored two-letter vocabulary to the archive
// enum. An unrecognised value reads as unspecified, which the format defines as
// a regular cash dividend.
func dividendTypeFromString(s string) archivev1.DividendType {
	if v, ok := archivev1.DividendType_value[s]; ok {
		return archivev1.DividendType(v)
	}
	return archivev1.DividendType_DIVIDEND_TYPE_UNSPECIFIED
}

// ImportCorporateEvents creates an async job to upsert the corporate event part
// of a system archive. The serialized request is persisted to the DB and
// processed by the ingestion worker. Admin only.
func (s *Server) ImportCorporateEvents(ctx context.Context, req *apiv1.ImportCorporateEventsRequest) (*apiv1.ImportCorporateEventsResponse, error) {
	u, authErr := auth.RequireAdmin(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if err := archive.CheckEnvelope(req.GetEnvelope(), archivev1.ArchiveKind_SYSTEM); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is
			// out of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(req.GetCorporateEvents().GetGroups()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no corporate event groups provided")
	}
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("serialize request: %v", err))
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:  u.ID,
		JobType: db.JobTypeCorporateEvent,
		Payload: payload,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, db.JobTypeCorporateEvent); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportCorporateEventsResponse{JobId: jobID}, nil
}

// ListUnhandledCorporateEvents returns corporate events that could not be
// automatically processed and require admin review. Admin only.
func (s *Server) ListUnhandledCorporateEvents(ctx context.Context, req *apiv1.ListUnhandledCorporateEventsRequest) (*apiv1.ListUnhandledCorporateEventsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	events, total, nextToken, err := s.db.ListUnhandledCorporateEvents(ctx, req.GetIncludeResolved(), pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &apiv1.ListUnhandledCorporateEventsResponse{
		TotalCount:    total,
		NextPageToken: nextToken,
	}
	for _, e := range events {
		pe := &apiv1.UnhandledCorporateEvent{
			Id:           e.ID,
			InstrumentId: e.InstrumentID,
			EventType:    e.EventType,
			Detail:       e.Detail,
			Resolved:     e.Resolved,
			CreatedAt:    timestamppb.New(e.CreatedAt),
		}
		if e.ExDate != nil {
			pe.ExDate = e.ExDate.Format("2006-01-02")
		}
		if e.Data != nil {
			pe.Data = string(e.Data)
		}
		// Resolve instrument name for display.
		inst, _ := s.db.GetInstrument(ctx, e.InstrumentID)
		if inst != nil && inst.Name != nil {
			pe.InstrumentName = *inst.Name
		}
		resp.Events = append(resp.Events, pe)
	}
	return resp, nil
}

// CountUnhandledCorporateEvents returns the number of unresolved corporate
// events. Admin only.
func (s *Server) CountUnhandledCorporateEvents(ctx context.Context, _ *apiv1.CountUnhandledCorporateEventsRequest) (*apiv1.CountUnhandledCorporateEventsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	count, err := s.db.CountUnhandledCorporateEvents(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.CountUnhandledCorporateEventsResponse{Count: count}, nil
}

// ResolveUnhandledCorporateEvent marks an unhandled corporate event as
// resolved. Admin only.
func (s *Server) ResolveUnhandledCorporateEvent(ctx context.Context, req *apiv1.ResolveUnhandledCorporateEventRequest) (*apiv1.ResolveUnhandledCorporateEventResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id required")
	}
	if err := s.db.ResolveUnhandledCorporateEvent(ctx, req.GetId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.ResolveUnhandledCorporateEventResponse{}, nil
}
