package api

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
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

// archiveInstrument converts one export row to its archive form.
//
// Not carried: the server UUID, which means nothing in another instance; the
// denormalized exchange column, derived from the MIC and the identifiers; and
// the joined exchange reference data, which exists so the SPA need not fetch it
// separately.
func archiveInstrument(row *db.InstrumentRow) *archivev1.Instrument {
	// Both grains, flattened. An archive instrument states one currency and so
	// names one line, and dropping its ticker would produce a file that cannot
	// re-resolve what it exported. Naming a listing outright, so a file can carry
	// more than one line of a security, is 0151.
	all := row.AllIdentifiers()
	identifiers := make([]*archivev1.Identifier, 0, len(all))
	for _, idn := range all {
		identifiers = append(identifiers, &archivev1.Identifier{
			Type:      identifierTypeFromString(idn.Ref.Type),
			Value:     idn.Ref.Value,
			Domain:    idn.Ref.Domain,
			Canonical: idn.Canonical,
			ValidFrom: optDate(idn.ValidFrom),
			// A name the instrument has given up travels too. Dropping it would
			// leave a file exported before a split naming a symbol the imported
			// instance has never heard of.
			ValidBefore: optDate(idn.ValidBefore),
		})
	}
	// The recorded output of the identifier lookups. Carrying them is the point
	// of the archive: a restored instrument the fetchers can address by the
	// provider's own identifier costs no second lookup.
	// Both grains, for the reason above: the archive exists so a restored
	// instrument needs no plugin call, and every provider identifier that exists
	// today names a listing.
	allProv := row.AllProviderIdentifiers()
	providerIdentifiers := make([]*archivev1.ProviderIdentifier, 0, len(allProv))
	for _, pi := range allProv {
		providerIdentifiers = append(providerIdentifiers, &archivev1.ProviderIdentifier{
			Provider:       pi.Provider,
			IdentifierType: pi.Type,
			Value:          pi.Value,
			Domain:         pi.Domain,
		})
	}
	out := &archivev1.Instrument{
		Name:                optStr(row.Name),
		ExchangeMic:         optStr(row.ExchangeMIC),
		Identifiers:         identifiers,
		ProviderIdentifiers: providerIdentifiers,
		Cik:                 optStr(row.CIK),
		SicCode:             optStr(row.SICCode),
		ValidFrom:           optDate(row.ValidFrom),
		ValidBefore:         optDate(row.ValidBefore),
		Strike:              decStrPtr(row.Strike),
		Expiry:              optDate(row.Expiry),
		PutCall:             optStr(row.PutCall),
	}
	if row.AssetClass != nil {
		out.AssetClass = db.StrToAssetClass(*row.AssetClass)
	}
	if row.Currency != nil {
		out.Currency = *row.Currency
	}
	if row.Underlying != nil {
		out.Underlying = &archivev1.InstrumentRef{
			Type:   identifierTypeFromString(row.Underlying.Type),
			Value:  row.Underlying.Value,
			Domain: row.Underlying.Domain,
		}
	}
	// Absent means the column default of 1, so the ordinary instrument says
	// nothing and only a split-adjusted deliverable writes a value.
	if !row.ContractMultiplier.Equal(decimal.NewFromInt(1)) {
		out.ContractMultiplier = proto.String(decStr(row.ContractMultiplier))
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
