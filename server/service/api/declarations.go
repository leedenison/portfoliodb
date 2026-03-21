package api

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const dateFormat = "2006-01-02"

// ListHoldingDeclarations lists all declarations for the authenticated user.
func (s *Server) ListHoldingDeclarations(ctx context.Context, req *apiv1.ListHoldingDeclarationsRequest) (*apiv1.ListHoldingDeclarationsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListHoldingDeclarations(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	decls := make([]*apiv1.HoldingDeclaration, len(rows))
	for i, r := range rows {
		decls[i] = &apiv1.HoldingDeclaration{
			Id:           r.ID,
			Broker:       r.Broker,
			Account:      r.Account,
			InstrumentId: r.InstrumentID,
			DeclaredQty:  r.DeclaredQty,
			AsOfDate:     r.AsOfDate.Format(dateFormat),
		}
		inst, err := s.db.GetInstrument(ctx, r.InstrumentID)
		if err == nil && inst != nil {
			decls[i].Instrument = instrumentRowToProto(inst)
		}
	}
	return &apiv1.ListHoldingDeclarationsResponse{Declarations: decls}, nil
}

// CreateHoldingDeclaration creates a declaration and computes the INITIALIZE tx.
func (s *Server) CreateHoldingDeclaration(ctx context.Context, req *apiv1.CreateHoldingDeclarationRequest) (*apiv1.CreateHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if req.GetBroker() == "" || req.GetInstrumentId() == "" || req.GetDeclaredQty() == "" || req.GetAsOfDate() == "" {
		return nil, status.Error(codes.InvalidArgument, "broker, instrument_id, declared_qty, and as_of_date are required")
	}
	asOfDate, err := time.Parse(dateFormat, req.GetAsOfDate())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid as_of_date: %v", err)
	}
	if _, err := strconv.ParseFloat(req.GetDeclaredQty(), 64); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid declared_qty: %v", err)
	}

	// Precondition: at least one real tx must exist
	startDate, err := s.db.GetPortfolioStartDate(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if startDate == nil {
		return nil, status.Error(codes.FailedPrecondition, "no real transactions exist; upload transactions before declaring opening balances")
	}
	// Precondition: as_of_date >= portfolio start date
	startDay := startDate.Truncate(24 * time.Hour)
	if asOfDate.Before(startDay) {
		return nil, status.Errorf(codes.InvalidArgument, "as_of_date must be on or after the portfolio start date (%s)", startDay.Format(dateFormat))
	}

	row, err := s.db.CreateHoldingDeclaration(ctx, u.ID, req.GetBroker(), req.GetAccount(), req.GetInstrumentId(), req.GetDeclaredQty(), asOfDate)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if err := s.computeAndUpsertInitializeTx(ctx, u.ID, row.Broker, row.Account, row.InstrumentID, req.GetDeclaredQty(), asOfDate, *startDate); err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	decl := &apiv1.HoldingDeclaration{
		Id:           row.ID,
		Broker:       row.Broker,
		Account:      row.Account,
		InstrumentId: row.InstrumentID,
		DeclaredQty:  row.DeclaredQty,
		AsOfDate:     row.AsOfDate.Format(dateFormat),
	}
	return &apiv1.CreateHoldingDeclarationResponse{Declaration: decl}, nil
}

// UpdateHoldingDeclaration updates a declaration and recomputes the INITIALIZE tx.
func (s *Server) UpdateHoldingDeclaration(ctx context.Context, req *apiv1.UpdateHoldingDeclarationRequest) (*apiv1.UpdateHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if req.GetId() == "" || req.GetDeclaredQty() == "" || req.GetAsOfDate() == "" {
		return nil, status.Error(codes.InvalidArgument, "id, declared_qty, and as_of_date are required")
	}
	asOfDate, err := time.Parse(dateFormat, req.GetAsOfDate())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid as_of_date: %v", err)
	}
	if _, err := strconv.ParseFloat(req.GetDeclaredQty(), 64); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid declared_qty: %v", err)
	}

	// Verify ownership
	existing, err := s.db.GetHoldingDeclaration(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}
	if existing.UserID != u.ID {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}

	startDate, err := s.db.GetPortfolioStartDate(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if startDate == nil {
		return nil, status.Error(codes.FailedPrecondition, "no real transactions exist")
	}
	startDay := startDate.Truncate(24 * time.Hour)
	if asOfDate.Before(startDay) {
		return nil, status.Errorf(codes.InvalidArgument, "as_of_date must be on or after the portfolio start date (%s)", startDay.Format(dateFormat))
	}

	row, err := s.db.UpdateHoldingDeclaration(ctx, req.GetId(), req.GetDeclaredQty(), asOfDate)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if err := s.computeAndUpsertInitializeTx(ctx, u.ID, row.Broker, row.Account, row.InstrumentID, req.GetDeclaredQty(), asOfDate, *startDate); err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	decl := &apiv1.HoldingDeclaration{
		Id:           row.ID,
		Broker:       row.Broker,
		Account:      row.Account,
		InstrumentId: row.InstrumentID,
		DeclaredQty:  row.DeclaredQty,
		AsOfDate:     row.AsOfDate.Format(dateFormat),
	}
	return &apiv1.UpdateHoldingDeclarationResponse{Declaration: decl}, nil
}

// DeleteHoldingDeclaration deletes a declaration and its INITIALIZE tx.
func (s *Server) DeleteHoldingDeclaration(ctx context.Context, req *apiv1.DeleteHoldingDeclarationRequest) (*apiv1.DeleteHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	existing, err := s.db.GetHoldingDeclaration(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}
	if existing.UserID != u.ID {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}
	if err := s.db.DeleteHoldingDeclaration(ctx, req.GetId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := s.db.DeleteInitializeTx(ctx, u.ID, existing.Broker, existing.Account, existing.InstrumentID); err != nil {
		return nil, status.Errorf(codes.Internal, "delete INITIALIZE tx: %v", err)
	}
	return &apiv1.DeleteHoldingDeclarationResponse{}, nil
}

// computeAndUpsertInitializeTx computes the INITIALIZE quantity and upserts the synthetic tx.
func (s *Server) computeAndUpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQtyStr string, asOfDate time.Time, startDate time.Time) error {
	declaredQty, err := strconv.ParseFloat(declaredQtyStr, 64)
	if err != nil {
		return fmt.Errorf("parse declared_qty: %w", err)
	}
	startDay := startDate.Truncate(24 * time.Hour)
	endOfAsOf := asOfDate.Add(24*time.Hour - time.Nanosecond)
	runningBalance, err := s.db.ComputeRunningBalance(ctx, userID, broker, account, instrumentID, startDay, endOfAsOf)
	if err != nil {
		return fmt.Errorf("compute running balance: %w", err)
	}
	initQty := declaredQty - runningBalance
	return s.db.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, startDay, initQty)
}
