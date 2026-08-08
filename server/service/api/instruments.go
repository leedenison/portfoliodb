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
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
)

// restoreIdentityAsOf carries an exported identity_as_of back onto an imported
// instrument. Without it a round trip would leave the column NULL and an option
// that had already been split-adjusted would look unadjusted to the retroactive
// adjustment pass, which would adjust it a second time. A payload with no
// identity_as_of leaves the stored value alone.
func (s *Server) restoreIdentityAsOf(ctx context.Context, instrumentID string, ts *timestamppb.Timestamp) error {
	if instrumentID == "" || ts == nil || !ts.IsValid() {
		return nil
	}
	return s.db.SetIdentityAsOf(ctx, instrumentID, ts.AsTime())
}

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

// ExportInstruments streams the instrument part of an admin archive: the
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
			Envelope: archive.NewEnvelope("", archivev1.ArchiveKind_ADMIN),
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
// Three passes: non-derivatives, then each derivative's underlying reference,
// then the derivatives themselves. A reference names an instrument by identifier
// rather than by position, so the passes index the part up front and the order
// the file was written in does not matter.
func (s *Server) ImportInstruments(ctx context.Context, req *apiv1.ImportInstrumentsRequest) (*apiv1.ImportInstrumentsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if err := archive.CheckEnvelope(req.GetEnvelope(), archivev1.ArchiveKind_ADMIN); err != nil {
		var ve *archive.VersionError
		if errors.As(err, &ve) {
			// The request is well formed and this server is the thing that is out
			// of date, which is a precondition rather than a bad argument.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	instruments := req.GetInstruments().GetInstruments()
	if len(instruments) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no instruments provided")
	}

	byRef := make(map[instKey]int, len(instruments))
	for i, inst := range instruments {
		for _, idf := range inst.GetIdentifiers() {
			byRef[refKey(idf.GetType(), idf.GetValue(), idf.GetDomain())] = i
		}
	}

	var errs []*apiv1.ImportInstrumentError
	seenKeys := make(map[string]struct{})
	ensuredIDs := make([]string, len(instruments))
	var ensuredCount int32

	// Pass 1: non-derivatives, which every derivative's underlying is one of.
	for i, inst := range instruments {
		if isDerivative(inst.GetAssetClass()) {
			continue
		}
		id, err := s.ensureArchiveInstrument(ctx, inst, "", i, seenKeys, &errs)
		if err != nil || id == "" {
			continue
		}
		ensuredIDs[i] = id
		ensuredCount++
	}

	// Pass 2: resolve each derivative's underlying to an instrument id. The
	// archive says the underlying appears in the same part, so that is where the
	// reference is looked up first; falling back to the database lets a partial
	// file import against an instance that already knows the underlying.
	underlyingIDByIndex := make(map[int]string, len(instruments))
	for i, inst := range instruments {
		if !isDerivative(inst.GetAssetClass()) {
			continue
		}
		ref := inst.GetUnderlying()
		if ref == nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "OPTION/FUTURE requires an underlying reference"})
			continue
		}
		key := refKey(ref.GetType(), ref.GetValue(), ref.GetDomain())
		if j, ok := byRef[key]; ok && ensuredIDs[j] != "" {
			underlyingIDByIndex[i] = ensuredIDs[j]
			continue
		}
		found, err := s.db.FindInstrumentByIdentifier(ctx, typev1.IdentifierType_name[int32(ref.GetType())], ref.GetDomain(), ref.GetValue())
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "underlying: " + err.Error()})
			continue
		}
		if found == "" {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "underlying " + ref.GetValue() + " is in neither this archive nor this instance"})
			continue
		}
		underlyingIDByIndex[i] = found
	}

	// Pass 3: the derivatives, whose underlyings now exist.
	for i, inst := range instruments {
		if !isDerivative(inst.GetAssetClass()) {
			continue
		}
		underlyingID, ok := underlyingIDByIndex[i]
		if !ok {
			continue // already reported in pass 2
		}
		id, err := s.ensureArchiveInstrument(ctx, inst, underlyingID, i, seenKeys, &errs)
		if err != nil || id == "" {
			continue
		}
		ensuredIDs[i] = id
		ensuredCount++
	}
	// No fetcher triggers here. Imported instruments have no holdings yet, so
	// there are no price gaps to fill. Corporate events (splits, dividends)
	// are picked up by the daily corporate event fetch cycle.
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensuredCount, Errors: errs}, nil
}

// ensureArchiveInstrument ensures one archive instrument, restoring the values
// nothing recomputes: the option terms, the deliverable multiplier and the
// identity vintage. It returns "" when it appended an error rather than storing
// anything.
func (s *Server) ensureArchiveInstrument(ctx context.Context, inst *archivev1.Instrument, underlyingID string, i int, seenKeys map[string]struct{}, errs *[]*apiv1.ImportInstrumentError) (string, error) {
	fail := func(msg string) (string, error) {
		*errs = append(*errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: msg})
		return "", nil
	}
	if len(inst.GetIdentifiers()) == 0 {
		return fail("at least one identifier required")
	}
	idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
	for _, idf := range inst.GetIdentifiers() {
		typeStr := typev1.IdentifierType_name[int32(idf.GetType())]
		key := typeStr + "\x00" + idf.GetValue()
		if _, ok := seenKeys[key]; ok {
			return fail("duplicate (type, value) in payload")
		}
		seenKeys[key] = struct{}{}
		idns = append(idns, db.IdentifierInput{Type: typeStr, Domain: idf.GetDomain(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
	}
	opts, err := archiveOptionFields(inst)
	if err != nil {
		return fail(err.Error())
	}
	id, err := s.db.EnsureInstrument(ctx, db.AssetClassToStr(inst.GetAssetClass()), inst.GetExchangeMic(), inst.GetCurrency(),
		inst.GetName(), inst.GetCik(), inst.GetSicCode(), idns, underlyingID,
		archiveDate(inst.ValidFrom), archiveDate(inst.ValidBefore), opts)
	if err != nil {
		return fail(err.Error())
	}
	if inst.ContractMultiplier != nil {
		m, err := decimal.NewFromString(inst.GetContractMultiplier())
		if err != nil {
			return fail("contract_multiplier: " + err.Error())
		}
		if err := s.db.SetContractMultiplier(ctx, id, m); err != nil {
			return fail("contract_multiplier: " + err.Error())
		}
	}
	if err := s.restoreIdentityAsOf(ctx, id, inst.GetIdentityAsOf()); err != nil {
		return fail("identity_as_of: " + err.Error())
	}
	return id, nil
}

// archiveOptionFields reads the denormalized OCC components off an archive
// instrument. They travel together or not at all: a strike with no expiry
// describes no real contract, so a part-stated set is an error rather than a
// silent half-restore.
func archiveOptionFields(inst *archivev1.Instrument) (*db.OptionFields, error) {
	n := 0
	for _, set := range []bool{inst.Strike != nil, inst.Expiry != nil, inst.PutCall != nil} {
		if set {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	if n < 3 {
		return nil, errors.New("strike, expiry and put_call are stated together or not at all")
	}
	strike, err := decimal.NewFromString(inst.GetStrike())
	if err != nil {
		return nil, errors.New("strike: " + err.Error())
	}
	expiry, err := time.Parse("2006-01-02", inst.GetExpiry())
	if err != nil {
		return nil, errors.New("expiry: " + err.Error())
	}
	return &db.OptionFields{Strike: strike, Expiry: expiry, PutCall: inst.GetPutCall()}, nil
}

// archiveDate parses an optional "YYYY-MM-DD" archive date. An unparseable
// value is treated as absent; protovalidate has already refused the document if
// it did not match the pattern.
func archiveDate(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil
	}
	return &t
}

// refKey names an instrument the way a file names it, for matching an
// underlying reference against the identifiers stated elsewhere in the part.
func refKey(t typev1.IdentifierType, value, domain string) instKey {
	return instKey{typ: typev1.IdentifierType_name[int32(t)], value: value, domain: domain}
}

func isDerivative(ac typev1.AssetClass) bool {
	return ac == typev1.AssetClass_OPTION || ac == typev1.AssetClass_FUTURE
}
