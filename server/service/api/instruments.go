package api

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
)

// ListInstruments returns instruments sorted alphabetically with optional search. Any authenticated user.
func (s *Server) ListInstruments(ctx context.Context, req *apiv1.ListInstrumentsRequest) (*apiv1.ListInstrumentsResponse, error) {
	if _, authErr := auth.RequireUser(ctx); authErr != nil {
		return nil, authErr
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var acStrs []string
	for _, ac := range req.GetAssetClasses() {
		acStrs = append(acStrs, db.AssetClassToStr(ac))
	}
	rows, totalCount, nextToken, err := s.db.ListInstruments(ctx, req.GetSearch(), acStrs, pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	instruments := make([]*apiv1.Instrument, 0, len(rows))
	for _, row := range rows {
		instruments = append(instruments, instrumentRowToProto(row))
	}
	return &apiv1.ListInstrumentsResponse{
		Instruments:   instruments,
		NextPageToken: nextToken,
		TotalCount:    totalCount,
	}, nil
}

// ExportInstruments streams the instrument part of a system archive: the
// envelope first, then one instrument per row. Admin only.
//
// A derivative names its underlying by identifier rather than nesting it, so a
// shared underlying appears once and the order the list is written in carries no
// meaning. The query guarantees every named underlying is itself in the stream,
// including one the caller's asset-class filter would have excluded.
func (s *Server) ExportInstruments(req *apiv1.ExportInstrumentsRequest, stream apiv1.ApiService_ExportInstrumentsServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	var acStrs []string
	for _, ac := range req.GetAssetClasses() {
		acStrs = append(acStrs, db.AssetClassToStr(ac))
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange(), acStrs)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// source_instance is left empty: nothing keys off it, and this build has no
	// configured identity to put there.
	if err := stream.Send(&apiv1.ExportInstrumentsResponse{
		Item: &apiv1.ExportInstrumentsResponse_Envelope{
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_SYSTEM),
		},
	}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := stream.Send(&apiv1.ExportInstrumentsResponse{
			Item: &apiv1.ExportInstrumentsResponse_Instrument{Instrument: archiveInstrument(row)},
		}); err != nil {
			return err
		}
	}
	return nil
}

// archiveInstrument converts one export row to its archive form.
//
// Not carried: the server UUID, which means nothing in another instance; the
// denormalized exchange column, derived from the MIC and the identifiers; and
// the joined exchange reference data, which exists so the SPA need not fetch it
// separately.
func archiveInstrument(row *db.InstrumentRow) *archivev1.Instrument {
	identifiers := make([]*archivev1.Identifier, 0, len(row.Identifiers))
	for _, idn := range row.Identifiers {
		identifiers = append(identifiers, &archivev1.Identifier{
			Type:      identifierTypeFromString(idn.Type),
			Value:     idn.Value,
			Domain:    idn.Domain,
			Canonical: idn.Canonical,
		})
	}
	out := &archivev1.Instrument{
		Name:        optStr(row.Name),
		ExchangeMic: optStr(row.ExchangeMIC),
		Identifiers: identifiers,
		Cik:         optStr(row.CIK),
		SicCode:     optStr(row.SICCode),
		ValidFrom:   optDate(row.ValidFrom),
		ValidBefore: optDate(row.ValidBefore),
		Strike:      decStrPtr(row.Strike),
		Expiry:      optDate(row.Expiry),
		PutCall:     optStr(row.PutCall),
	}
	if row.AssetClass != nil {
		out.AssetClass = db.StrToAssetClass(*row.AssetClass)
	}
	if row.Currency != nil {
		out.Currency = *row.Currency
	}
	if row.UnderlyingIdentifierType != nil {
		out.Underlying = &archivev1.InstrumentRef{
			Type:   identifierTypeFromString(*row.UnderlyingIdentifierType),
			Value:  derefStr(row.UnderlyingIdentifierValue),
			Domain: derefStr(row.UnderlyingIdentifierDomain),
		}
	}
	// Absent means the column default of 1, so the ordinary instrument says
	// nothing and only a split-adjusted deliverable writes a value.
	if !row.ContractMultiplier.Equal(decimal.NewFromInt(1)) {
		out.ContractMultiplier = proto.String(decStr(row.ContractMultiplier))
	}
	if row.IdentityAsOf != nil {
		out.IdentityAsOf = timestamppb.New(*row.IdentityAsOf)
	}
	return out
}

// optStr renders an optional string field, treating empty as absent: the
// archive writes a field only when it has a value.
func optStr(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// optDate renders an optional date as the archive's "YYYY-MM-DD".
func optDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	return proto.String(t.Format("2006-01-02"))
}

// ImportInstruments ensures the instruments of an archive's instrument part
// exist, finding or creating each by its identifiers. Admin only.
//
// It applies the part synchronously and reports per-instrument problems in its
// own response, so it reports through a detached reporter: there is no job to
// record them against.
func (s *Server) ImportInstruments(ctx context.Context, req *apiv1.ImportInstrumentsRequest) (*apiv1.ImportInstrumentsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if err := archive.CheckEnvelope(req.GetEnvelope(), archivev1.ArchiveKind_SYSTEM); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is out
			// of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	part := req.GetInstruments()
	if len(part.GetInstruments()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no instruments provided")
	}

	rep := archiveimport.NewDetachedReporter()
	ensured, err := archiveimport.InstrumentPart(ctx, s.db, part, rep)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	errs := make([]*apiv1.ImportInstrumentError, 0, len(rep.Errors()))
	for _, e := range rep.Errors() {
		errs = append(errs, &apiv1.ImportInstrumentError{Index: e.GetRowIndex(), Message: e.GetMessage()})
	}
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensured, Errors: errs}, nil
}
