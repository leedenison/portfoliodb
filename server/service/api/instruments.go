package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExportInstruments streams instruments that have at least one canonical identifier. Optional exchange filter. Admin only.
func (s *Server) ExportInstruments(req *apiv1.ExportInstrumentsRequest, stream apiv1.ApiService_ExportInstrumentsServer) error {
	ctx := stream.Context()
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange())
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, row := range rows {
		protoInst := instrumentRowToProto(row)
		// For derivatives, include nested underlying with canonical identifiers (no reliance on internal ID for import).
		if row.UnderlyingID != "" {
			underlying, err := s.db.GetInstrument(ctx, row.UnderlyingID)
			if err == nil && underlying != nil {
				protoInst.Underlying = instrumentRowToProto(underlying)
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
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	var errs []*apiv1.ImportInstrumentError
	seenKeys := make(map[string]struct{})
	var ensuredCount int32
	instruments := req.GetInstruments()

	// Pass 1: ensure all non-derivatives (and ensure nested underlyings from derivatives).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		isDerivative := ac == "OPTION" || ac == "FUTURE"
		if isDerivative {
			continue
		}
		if len(inst.GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "at least one identifier required"})
			continue
		}
		dup := false
		for _, idf := range inst.GetIdentifiers() {
			key := idf.GetType() + "\x00" + idf.GetValue()
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
			idns = append(idns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, inst.GetAssetClass(), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), idns, "", protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
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
		if ac != "OPTION" && ac != "FUTURE" {
			continue
		}
		if inst.GetUnderlying() == nil || len(inst.GetUnderlying().GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "OPTION/FUTURE requires nested underlying with at least one identifier"})
			continue
		}
		u := inst.GetUnderlying()
		uIdns := make([]db.IdentifierInput, 0, len(u.GetIdentifiers()))
		for _, idf := range u.GetIdentifiers() {
			uIdns = append(uIdns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		underlyingID, err := s.db.EnsureInstrument(ctx, u.GetAssetClass(), u.GetExchange(), u.GetCurrency(), u.GetName(), uIdns, "", protoValidFrom(u.GetValidFrom()), protoValidTo(u.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "underlying: " + err.Error()})
			continue
		}
		underlyingIDByIndex[int32(i)] = underlyingID
	}

	// Pass 2: ensure derivatives (underlyings already ensured).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		if ac != "OPTION" && ac != "FUTURE" {
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
			key := idf.GetType() + "\x00" + idf.GetValue()
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
			idns = append(idns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, inst.GetAssetClass(), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), idns, underlyingID, protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensuredCount, Errors: errs}, nil
}
