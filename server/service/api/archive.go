package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
)

// ImportSystemArchive queues one whole system archive and returns the job to
// poll. Admin only.
//
// The parts are applied in the worker rather than in this request, which is what
// makes an import survive the admin closing the tab: the only thing the client
// has to hold on to is the job id, and the per-part results are readable from
// GetJob for as long as the job row lives.
func (s *Server) ImportSystemArchive(ctx context.Context, req *apiv1.ImportSystemArchiveRequest) (*apiv1.ImportSystemArchiveResponse, error) {
	u, authErr := auth.RequireAdmin(ctx)
	if authErr != nil {
		return nil, authErr
	}
	a := req.GetArchive()
	if err := archive.CheckEnvelope(a.GetEnvelope(), archivev1.ArchiveKind_SYSTEM); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is out
			// of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	parts := presentParts(a)
	if len(parts) == 0 {
		return nil, status.Error(codes.InvalidArgument, "archive carries no parts")
	}

	// The document alone is stored: the filename is a label for the job row and
	// the worker has no use for it.
	payload, err := proto.Marshal(a)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	jobID, err := s.db.CreateJob(ctx, db.CreateJobParams{
		UserID:   u.ID,
		JobType:  db.JobTypeSystemArchive,
		Filename: req.GetFilename(),
		Payload:  payload,
		Parts:    parts,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.enqueueJob(jobID, db.JobTypeSystemArchive); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &apiv1.ImportSystemArchiveResponse{JobId: jobID}, nil
}

// presentParts names the parts a document carries, in restore order.
//
// Presence, not emptiness: a section present but empty says the export included
// it and there was nothing, which is a different statement from a section that
// was never included, and the import honours the difference.
func presentParts(a *archivev1.SystemArchive) []archivev1.ArchivePart {
	var parts []archivev1.ArchivePart
	if a.GetInstruments() != nil {
		parts = append(parts, archivev1.ArchivePart_INSTRUMENTS)
	}
	if a.GetPrices() != nil {
		parts = append(parts, archivev1.ArchivePart_PRICES)
	}
	if a.GetCorporateEvents() != nil {
		parts = append(parts, archivev1.ArchivePart_CORPORATE_EVENTS)
	}
	if a.GetInflationIndices() != nil {
		parts = append(parts, archivev1.ArchivePart_INFLATION_INDICES)
	}
	if a.GetFetchBlocks() != nil {
		parts = append(parts, archivev1.ArchivePart_FETCH_BLOCKS)
	}
	if a.GetUnhandledEvents() != nil {
		parts = append(parts, archivev1.ArchivePart_UNHANDLED_EVENTS)
	}
	return parts
}

// ExportSystemArchive streams one system archive: the envelope, then the
// selected parts in restore order. Admin only.
//
// The envelope is sent once and first, so exported_at is one reading of the
// clock for the whole document. Knowledge time that differs between a file's own
// parts is not knowledge time.
func (s *Server) ExportSystemArchive(req *apiv1.ExportSystemArchiveRequest, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	// source_instance is left empty: nothing keys off it, and this build has no
	// configured identity to put there.
	if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
		Item: &apiv1.ExportSystemArchiveResponse_Envelope{
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_SYSTEM),
		},
	}); err != nil {
		return err
	}
	for _, part := range requestedParts(req.GetParts()) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_PartBegin{
				PartBegin: &apiv1.ArchivePartBegin{Part: part},
			},
		}); err != nil {
			return err
		}
		var err error
		switch part {
		case archivev1.ArchivePart_INSTRUMENTS:
			err = s.sendInstrumentPart(ctx, req, stream)
		case archivev1.ArchivePart_PRICES:
			err = s.sendPricePart(ctx, stream)
		case archivev1.ArchivePart_CORPORATE_EVENTS:
			err = s.sendCorporateEventPart(ctx, stream)
		case archivev1.ArchivePart_INFLATION_INDICES:
			err = s.sendInflationPart(ctx, stream)
		case archivev1.ArchivePart_FETCH_BLOCKS:
			err = s.sendFetchBlockPart(ctx, stream)
		case archivev1.ArchivePart_UNHANDLED_EVENTS:
			err = s.sendUnhandledEventPart(ctx, stream)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// requestedParts dedupes the menu and puts it in restore order. Order in the
// document is the order it is applied, so it is the server's to decide and not
// the caller's to state.
func requestedParts(req []archivev1.ArchivePart) []archivev1.ArchivePart {
	seen := make(map[archivev1.ArchivePart]bool, len(req))
	for _, p := range req {
		seen[p] = true
	}
	var out []archivev1.ArchivePart
	for _, p := range []archivev1.ArchivePart{
		archivev1.ArchivePart_INSTRUMENTS,
		archivev1.ArchivePart_PRICES,
		archivev1.ArchivePart_CORPORATE_EVENTS,
		archivev1.ArchivePart_INFLATION_INDICES,
		archivev1.ArchivePart_FETCH_BLOCKS,
		archivev1.ArchivePart_UNHANDLED_EVENTS,
	} {
		if seen[p] {
			out = append(out, p)
		}
	}
	return out
}

// sendInstrumentPart streams the security master.
//
// A derivative names its underlying by identifier rather than nesting it, so a
// shared underlying appears once and the order the list is written in carries no
// meaning. The query guarantees every named underlying is itself in the stream,
// including one the caller's asset-class filter would have excluded.
func (s *Server) sendInstrumentPart(ctx context.Context, req *apiv1.ExportSystemArchiveRequest, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	var acStrs []string
	for _, ac := range req.GetAssetClasses() {
		acStrs = append(acStrs, db.AssetClassToStr(ac))
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange(), acStrs)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, row := range rows {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_Instrument{Instrument: archiveInstrument(row)},
		}); err != nil {
			return err
		}
	}
	return nil
}

// sendPricePart streams one group per instrument.
//
// The coverage nested in each group is not derivable from its rows: a span
// covering dates with no rows records that a provider was asked and had nothing.
func (s *Server) sendPricePart(ctx context.Context, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	coverage, err := s.db.ListPriceCoverageForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	rows, err := s.db.ListPricesForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, g := range priceGroups(rows, coverage) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_PriceGroup{PriceGroup: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// sendCorporateEventPart streams one group per instrument.
//
// The coverage nested in each group is not derivable from its events: events are
// sparse, so a span holding none of them records that a provider was asked and
// had nothing, which is a different statement from never having asked.
func (s *Server) sendCorporateEventPart(ctx context.Context, stream apiv1.ApiService_ExportSystemArchiveServer) error {
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
	for _, g := range corporateEventGroups(splits, dividends, coverage) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_CorporateEventGroup{CorporateEventGroup: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// sendInflationPart streams one group per currency.
//
// There is no coverage to send alongside the rows, and it is the only part
// where that is true: an index series is dense, so the rows say what is held
// and inflation_indices stores no coverage to contradict them.
func (s *Server) sendInflationPart(ctx context.Context, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	rows, err := s.db.ListInflationIndicesForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, g := range inflationGroups(rows) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_InflationGroup{InflationGroup: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// inflationGroups turns the flat export rows into one group per currency. The
// query orders by currency, so the grouping is a scan.
func inflationGroups(rows []db.InflationIndex) []*archivev1.InflationGroup {
	var out []*archivev1.InflationGroup
	var cur *archivev1.InflationGroup
	for _, r := range rows {
		if cur == nil || cur.GetCurrency() != r.Currency {
			cur = &archivev1.InflationGroup{Currency: r.Currency}
			out = append(out, cur)
		}
		cur.Rows = append(cur.Rows, &archivev1.InflationRow{
			Month:      r.Month.Format("2006-01-02"),
			IndexValue: r.IndexValue.String(),
			BaseYear:   int32(r.BaseYear),
		})
	}
	return out
}

// sendFetchBlockPart streams one group per instrument, carrying that
// instrument's blocks across both fetchers.
//
// The two tables travel in one part because they are the same statement about
// two fetchers. An instrument blocked in both appears once, with two blocks.
func (s *Server) sendFetchBlockPart(ctx context.Context, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	priceBlocks, err := s.db.ListPriceFetchBlocksForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	eventBlocks, err := s.db.ListCorporateEventFetchBlocksForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, g := range fetchBlockGroups(priceBlocks, eventBlocks) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_FetchBlockGroup{FetchBlockGroup: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// fetchBlockGroups merges the two tables into one group per instrument.
//
// Each query is ordered by identifier, but the two orders interleave rather
// than concatenate, so this is a merge by key rather than the scan the
// single-table exports use. Groups come out in the order their instrument was
// first seen, which is the price table's order and then whatever the event
// table adds.
func fetchBlockGroups(price, events []db.ExportFetchBlock) []*archivev1.FetchBlockGroup {
	var out []*archivev1.FetchBlockGroup
	byKey := make(map[instKey]*archivev1.FetchBlockGroup)

	add := func(b db.ExportFetchBlock, category typev1.PluginCategory) {
		k := instKey{b.IdentifierType, b.IdentifierValue, b.IdentifierDomain}
		g, ok := byKey[k]
		if !ok {
			g = &archivev1.FetchBlockGroup{
				Instrument: &archivev1.InstrumentRef{
					Type:   identifierTypeFromString(b.IdentifierType),
					Value:  b.IdentifierValue,
					Domain: b.IdentifierDomain,
				},
			}
			byKey[k] = g
			out = append(out, g)
		}
		g.Blocks = append(g.Blocks, &archivev1.FetchBlock{
			Category:       category,
			PluginId:       b.PluginID,
			Reason:         b.Reason,
			FirstBlockedAt: timestamppb.New(b.FirstBlockedAt),
		})
	}

	for _, b := range price {
		add(b, typev1.PluginCategory_PRICE)
	}
	for _, b := range events {
		add(b, typev1.PluginCategory_CORPORATE_EVENT)
	}
	return out
}

// sendUnhandledEventPart streams one group per instrument.
//
// Resolved and unresolved events both travel: the resolved flag is the
// irreplaceable half, but these rows are only ever created by a fetch detecting
// something it could not apply, and an import writes events from the file
// rather than fetching them. Nothing would re-create the queue.
func (s *Server) sendUnhandledEventPart(ctx context.Context, stream apiv1.ApiService_ExportSystemArchiveServer) error {
	rows, err := s.db.ListUnhandledCorporateEventsForExport(ctx)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, g := range unhandledEventGroups(rows) {
		if err := stream.Send(&apiv1.ExportSystemArchiveResponse{
			Item: &apiv1.ExportSystemArchiveResponse_UnhandledEventGroup{UnhandledEventGroup: g},
		}); err != nil {
			return err
		}
	}
	return nil
}

// unhandledEventGroups turns the flat export rows into one group per
// instrument. The query orders by identifier, so the grouping is a scan.
func unhandledEventGroups(rows []db.ExportUnhandledCorporateEvent) []*archivev1.UnhandledEventGroup {
	var out []*archivev1.UnhandledEventGroup
	var cur *archivev1.UnhandledEventGroup
	var curKey instKey
	for _, r := range rows {
		k := instKey{r.IdentifierType, r.IdentifierValue, r.IdentifierDomain}
		if cur == nil || k != curKey {
			cur = &archivev1.UnhandledEventGroup{
				Instrument: &archivev1.InstrumentRef{
					Type:   identifierTypeFromString(r.IdentifierType),
					Value:  r.IdentifierValue,
					Domain: r.IdentifierDomain,
				},
			}
			curKey = k
			out = append(out, cur)
		}
		e := &archivev1.UnhandledEvent{
			EventType:  r.EventType,
			Detail:     r.Detail,
			Resolved:   r.Resolved,
			DetectedAt: timestamppb.New(r.CreatedAt),
		}
		if r.ExDate != nil {
			e.ExDate = proto.String(r.ExDate.Format("2006-01-02"))
		}
		if len(r.Data) > 0 {
			e.DataJson = proto.String(string(r.Data))
		}
		cur.Events = append(cur.Events, e)
	}
	return out
}
