package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// ExportInstruments streams instruments that have at least one canonical identifier. Optional exchange filter. Admin only.
func (s *Server) ExportInstruments(req *apiv1.ExportInstrumentsRequest, stream apiv1.ApiService_ExportInstrumentsServer) error {
	ctx := stream.Context()
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return authErr
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange())
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	// Batch-load underlyings for derivatives
	underlyingIDs := make([]string, 0)
	for _, row := range rows {
		if row.UnderlyingID != nil && *row.UnderlyingID != "" {
			underlyingIDs = append(underlyingIDs, *row.UnderlyingID)
		}
	}
	underlyingByID := make(map[string]*db.InstrumentRow)
	if len(underlyingIDs) > 0 {
		underlyingRows, err := s.db.ListInstrumentsByIDs(ctx, underlyingIDs)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		for _, r := range underlyingRows {
			underlyingByID[r.ID] = r
		}
	}
	for _, row := range rows {
		protoInst := instrumentRowToProto(row)
		if row.UnderlyingID != nil && *row.UnderlyingID != "" {
			if u := underlyingByID[*row.UnderlyingID]; u != nil {
				protoInst.Underlying = instrumentRowToProto(u)
			}
		}
		if err := stream.Send(protoInst); err != nil {
			return err
		}
	}
	return nil
}

// ImportInstruments ensures the given instruments exist (find-or-create by identifiers). Two-pass: underlyings first, then derivatives. Admin only.
func (s *Server) ImportInstruments(ctx context.Context, req *apiv1.ImportInstrumentsRequest) (*apiv1.ImportInstrumentsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	var errs []*apiv1.ImportInstrumentError
	seenKeys := make(map[string]struct{})
	var ensuredCount int32
	instruments := req.GetInstruments()

	// Pass 1: ensure all non-derivatives (and ensure nested underlyings from derivatives).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		isDerivative := ac == apiv1.AssetClass_ASSET_CLASS_OPTION || ac == apiv1.AssetClass_ASSET_CLASS_FUTURE
		if isDerivative {
			continue
		}
		if len(inst.GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "at least one identifier required"})
			continue
		}
		dup := false
		for _, idf := range inst.GetIdentifiers() {
			typeStr := apiv1.IdentifierType_name[int32(idf.GetType())]
			key := typeStr + "\x00" + idf.GetValue()
			if _, ok := seenKeys[key]; ok {
				errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "duplicate (type, value) in payload"})
				dup = true
				break
			}
			seenKeys[key] = struct{}{}
		}
		if dup {
			continue
		}
		idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
		for _, idf := range inst.GetIdentifiers() {
			typeStr := apiv1.IdentifierType_name[int32(idf.GetType())]
			idns = append(idns, db.IdentifierInput{Type: typeStr, Domain: idf.GetDomain(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, db.AssetClassToStr(inst.GetAssetClass()), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), inst.GetCik(), inst.GetSicCode(), idns, "", protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}

	// For derivatives we need to ensure nested underlyings first (in pass 1 we skipped derivatives; ensure their underlyings here if not already in payload).
	underlyingIDByIndex := make(map[int32]string) // index -> resolved underlying_id for derivatives
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		if ac != apiv1.AssetClass_ASSET_CLASS_OPTION && ac != apiv1.AssetClass_ASSET_CLASS_FUTURE {
			continue
		}
		if inst.GetUnderlying() == nil || len(inst.GetUnderlying().GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "OPTION/FUTURE requires nested underlying with at least one identifier"})
			continue
		}
		u := inst.GetUnderlying()
		uIdns := make([]db.IdentifierInput, 0, len(u.GetIdentifiers()))
		for _, idf := range u.GetIdentifiers() {
			typeStr := apiv1.IdentifierType_name[int32(idf.GetType())]
			uIdns = append(uIdns, db.IdentifierInput{Type: typeStr, Domain: idf.GetDomain(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		underlyingID, err := s.db.EnsureInstrument(ctx, db.AssetClassToStr(u.GetAssetClass()), u.GetExchange(), u.GetCurrency(), u.GetName(), u.GetCik(), u.GetSicCode(), uIdns, "", protoValidFrom(u.GetValidFrom()), protoValidTo(u.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "underlying: " + err.Error()})
			continue
		}
		underlyingIDByIndex[int32(i)] = underlyingID
	}

	// Pass 2: ensure derivatives (underlyings already ensured).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		if ac != apiv1.AssetClass_ASSET_CLASS_OPTION && ac != apiv1.AssetClass_ASSET_CLASS_FUTURE {
			continue
		}
		underlyingID, ok := underlyingIDByIndex[int32(i)]
		if !ok {
			continue // already had an error in pass 1
		}
		if len(inst.GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "at least one identifier required"})
			continue
		}
		dup := false
		for _, idf := range inst.GetIdentifiers() {
			typeStr := apiv1.IdentifierType_name[int32(idf.GetType())]
			key := typeStr + "\x00" + idf.GetValue()
			if _, ok := seenKeys[key]; ok {
				errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "duplicate (type, value) in payload"})
				dup = true
				break
			}
			seenKeys[key] = struct{}{}
		}
		if dup {
			continue
		}
		idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
		for _, idf := range inst.GetIdentifiers() {
			typeStr := apiv1.IdentifierType_name[int32(idf.GetType())]
			idns = append(idns, db.IdentifierInput{Type: typeStr, Domain: idf.GetDomain(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, db.AssetClassToStr(inst.GetAssetClass()), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), inst.GetCik(), inst.GetSicCode(), idns, underlyingID, protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensuredCount, Errors: errs}, nil
}
