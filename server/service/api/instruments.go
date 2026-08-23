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
// Not carried: the server UUID, which means nothing in another instance; a
// listing's venues, derived from its own identifiers by trigger; the
// denormalized exchange column, derived from the MIC and the identifiers; and
// the joined exchange reference data, which exists so the SPA need not fetch it
// separately. Nor is there an instrument-level currency or validity interval:
// both are facts about a line, and the security's lifetime is the hull of its
// lines'.
func archiveInstrument(row *db.InstrumentRow) *archivev1.Instrument {
	// Each identifier at its own grain, which is where it is stored and what it
	// names. AllIdentifiers flattened the two together while a file could state
	// only one currency; a file that carries every line can put each name on the
	// line it names.
	out := &archivev1.Instrument{
		Name:                optStr(row.Name),
		Identifiers:         archiveIdentifiers(row.Identifiers),
		ProviderIdentifiers: archiveProviderIdentifiers(row.ProviderIdentifiers),
		Listings:            make([]*archivev1.Listing, 0, len(row.Listings)),
		Cik:                 optStr(row.CIK),
		SicCode:             optStr(row.SICCode),
		Strike:              decStrPtr(row.Strike),
		Expiry:              optDate(row.Expiry),
		PutCall:             optStr(row.PutCall),
	}
	for _, l := range row.Listings {
		out.Listings = append(out.Listings, &archivev1.Listing{
			Currency:            optStr(l.Currency),
			ValidFrom:           optDate(l.ValidFrom),
			ValidBefore:         optDate(l.ValidBefore),
			Identifiers:         archiveIdentifiers(l.Identifiers),
			ProviderIdentifiers: archiveProviderIdentifiers(l.ProviderIdentifiers),
		})
	}
	if row.AssetClass != nil {
		out.AssetClass = db.StrToAssetClass(*row.AssetClass)
	}
	if row.Underlying != nil {
		out.Underlying = &archivev1.InstrumentRef{
			Type:   identifierTypeFromString(row.Underlying.Type),
			Value:  row.Underlying.Value,
			Domain: row.Underlying.Domain,
			// Which line of it the contract delivers. The importing instance
			// would otherwise have to re-derive this from the derivative's own
			// symbology, and a file that states it cannot be read two ways.
			Currency: row.UnderlyingCurrency,
		}
	}
	// Absent means the column default of 1, so the ordinary instrument says
	// nothing and only a split-adjusted deliverable writes a value.
	if !row.ContractMultiplier.Equal(decimal.NewFromInt(1)) {
		out.ContractMultiplier = proto.String(decStr(row.ContractMultiplier))
	}
	return out
}

// archiveIdentifiers converts one grain's identifiers to their archive form.
//
// A name the thing has given up travels too. Dropping it would leave a file
// exported before a split naming a symbol the importing instance has never
// heard of, and would make an already-restated option look unrestated.
func archiveIdentifiers(in []db.IdentifierInput) []*archivev1.Identifier {
	out := make([]*archivev1.Identifier, 0, len(in))
	for _, idn := range in {
		out = append(out, &archivev1.Identifier{
			Type:        identifierTypeFromString(idn.Ref.Type),
			Value:       idn.Ref.Value,
			Domain:      idn.Ref.Domain,
			Canonical:   idn.Canonical,
			ValidFrom:   optDate(idn.ValidFrom),
			ValidBefore: optDate(idn.ValidBefore),
		})
	}
	return out
}

// archiveProviderIdentifiers converts one grain's provider identifiers.
//
// Carrying them is the point of the archive: a restored instrument the fetchers
// can address by the provider's own identifier costs no second lookup.
func archiveProviderIdentifiers(in []db.ProviderIdentifierInput) []*archivev1.ProviderIdentifier {
	out := make([]*archivev1.ProviderIdentifier, 0, len(in))
	for _, pi := range in {
		out = append(out, &archivev1.ProviderIdentifier{
			Provider:       pi.Provider,
			IdentifierType: pi.Type,
			Value:          pi.Value,
			Domain:         pi.Domain,
		})
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
