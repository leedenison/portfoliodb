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
	// Batch-load instruments to avoid N+1 queries.
	instIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		instIDs = append(instIDs, r.InstrumentID)
	}
	instByID := make(map[string]*apiv1.Instrument)
	if len(instIDs) > 0 {
		instRows, err := s.db.ListInstrumentsByIDs(ctx, instIDs)
		if err == nil {
			for _, ir := range instRows {
				instByID[ir.ID] = instrumentRowToProto(ir)
			}
		}
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
		if inst := instByID[r.InstrumentID]; inst != nil {
			decls[i].Instrument = inst
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

	init, err := s.computeInitializeValues(ctx, u.ID, req.GetBroker(), req.GetAccount(), req.GetInstrumentId(), req.GetDeclaredQty(), asOfDate, *startDate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	row, err := s.db.CreateDeclarationWithInitializeTx(ctx, u.ID, req.GetBroker(), req.GetAccount(), req.GetInstrumentId(), req.GetDeclaredQty(), asOfDate, init.txType, init.timestamp, init.quantity)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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

	init, err := s.computeInitializeValues(ctx, u.ID, existing.Broker, existing.Account, existing.InstrumentID, req.GetDeclaredQty(), asOfDate, *startDate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	row, err := s.db.UpdateDeclarationWithInitializeTx(ctx, req.GetId(), req.GetDeclaredQty(), asOfDate, u.ID, existing.Broker, existing.Account, existing.InstrumentID, init.txType, init.timestamp, init.quantity)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	if err := s.db.DeleteDeclarationWithInitializeTx(ctx, req.GetId(), u.ID, existing.Broker, existing.Account, existing.InstrumentID); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.DeleteHoldingDeclarationResponse{}, nil
}

// initializeValues holds the computed values for an INITIALIZE tx.
type initializeValues struct {
	timestamp time.Time
	quantity  float64
	txType    string
}

// computeInitializeValues computes the INITIALIZE tx timestamp, quantity, and tx type.
func (s *Server) computeInitializeValues(ctx context.Context, userID, broker, account, instrumentID, declaredQtyStr string, asOfDate time.Time, startDate time.Time) (*initializeValues, error) {
	declaredQty, err := strconv.ParseFloat(declaredQtyStr, 64)
	if err != nil {
		return nil, fmt.Errorf("parse declared_qty: %w", err)
	}
	startDay := startDate.Truncate(24 * time.Hour)
	dayAfterAsOf := asOfDate.AddDate(0, 0, 1)
	runningBalance, err := s.db.ComputeRunningBalance(ctx, userID, broker, account, instrumentID, startDay, dayAfterAsOf)
	if err != nil {
		return nil, fmt.Errorf("compute running balance: %w", err)
	}
	initQty := declaredQty - runningBalance
	var assetClass string
	inst, err := s.db.GetInstrument(ctx, instrumentID)
	if err == nil && inst != nil && inst.AssetClass != nil {
		assetClass = *inst.AssetClass
	}
	txType := initializeTxType(assetClass, initQty)
	return &initializeValues{timestamp: startDay, quantity: initQty, txType: txType}, nil
}

// initializeTxType returns the OFX tx type for an INITIALIZE tx based on asset class and quantity sign.
func initializeTxType(assetClass string, qty float64) string {
	buy, sell := "BUYOTHER", "SELLOTHER"
	switch assetClass {
	case "STOCK", "ETF":
		buy, sell = "BUYSTOCK", "SELLSTOCK"
	case "OPTION":
		buy, sell = "BUYOPT", "SELLOPT"
	case "MUTUAL_FUND":
		buy, sell = "BUYMF", "SELLMF"
	case "FIXED_INCOME":
		buy, sell = "BUYDEBT", "SELLDEBT"
	case "CASH":
		buy, sell = "JRNLFUND", "JRNLFUND"
	}
	if qty < 0 {
		return sell
	}
	return buy
}
