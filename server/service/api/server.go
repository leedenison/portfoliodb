package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements ApiService.
type Server struct {
	apiv1.UnimplementedApiServiceServer
	db db.DB
}

// NewServer returns a new API server.
func NewServer(database db.DB) *Server {
	return &Server{db: database}
}

// CreateUser creates or updates the user from stub token data (M01).
func (s *Server) CreateUser(ctx context.Context, req *apiv1.CreateUserRequest) (*apiv1.CreateUserResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.AuthSub == "" {
		return nil, status.Error(codes.Unauthenticated, "missing auth")
	}
	authSub := req.GetAuthSub()
	if authSub == "" {
		authSub = u.AuthSub
	}
	name := req.GetName()
	if name == "" {
		name = u.Name
	}
	email := req.GetEmail()
	if email == "" {
		email = u.Email
	}
	userID, err := s.db.GetOrCreateUser(ctx, authSub, name, email)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.CreateUserResponse{UserId: userID}, nil
}

// ListPortfolios returns portfolios owned by the authenticated user.
func (s *Server) ListPortfolios(ctx context.Context, req *apiv1.ListPortfoliosRequest) (*apiv1.ListPortfoliosResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	portfolios, nextToken, err := s.db.ListPortfolios(ctx, u.ID, pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.ListPortfoliosResponse{Portfolios: portfolios, NextPageToken: nextToken}, nil
}

// GetPortfolio returns a portfolio by ID if owned by the user.
func (s *Server) GetPortfolio(ctx context.Context, req *apiv1.GetPortfolioRequest) (*apiv1.GetPortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	portfolio, _, err := s.db.GetPortfolio(ctx, req.GetPortfolioId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if portfolio == nil {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	return &apiv1.GetPortfolioResponse{Portfolio: portfolio}, nil
}

// CreatePortfolio creates a portfolio for the authenticated user.
func (s *Server) CreatePortfolio(ctx context.Context, req *apiv1.CreatePortfolioRequest) (*apiv1.CreatePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name required")
	}
	portfolio, err := s.db.CreatePortfolio(ctx, u.ID, req.GetName())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.CreatePortfolioResponse{Portfolio: portfolio}, nil
}

// UpdatePortfolio updates a portfolio name if owned by the user.
func (s *Server) UpdatePortfolio(ctx context.Context, req *apiv1.UpdatePortfolioRequest) (*apiv1.UpdatePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	portfolio, err := s.db.UpdatePortfolio(ctx, req.GetPortfolioId(), req.GetName())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if portfolio == nil {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	return &apiv1.UpdatePortfolioResponse{Portfolio: portfolio}, nil
}

// DeletePortfolio deletes a portfolio if owned by the user.
func (s *Server) DeletePortfolio(ctx context.Context, req *apiv1.DeletePortfolioRequest) (*apiv1.DeletePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	if err := s.db.DeletePortfolio(ctx, req.GetPortfolioId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.DeletePortfolioResponse{}, nil
}

// ListTxs lists transactions for a portfolio owned by the user.
func (s *Server) ListTxs(ctx context.Context, req *apiv1.ListTxsRequest) (*apiv1.ListTxsResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var broker *apiv1.Broker
	if req.Broker != apiv1.Broker_BROKER_UNSPECIFIED {
		broker = &req.Broker
	}
	txs, nextToken, err := s.db.ListTxs(ctx, req.GetPortfolioId(), broker, req.PeriodFrom, req.PeriodTo, pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	for _, pt := range txs {
		if pt.GetTx().GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, pt.Tx.InstrumentId)
			if err == nil && inst != nil {
				pt.Instrument = instrumentRowToProto(inst)
			}
		}
	}
	return &apiv1.ListTxsResponse{Txs: txs, NextPageToken: nextToken}, nil
}

// GetHoldings returns holdings for a portfolio at a point in time.
func (s *Server) GetHoldings(ctx context.Context, req *apiv1.GetHoldingsRequest) (*apiv1.GetHoldingsResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	holdings, asOf, err := s.db.ComputeHoldings(ctx, req.GetPortfolioId(), req.AsOf)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	for _, h := range holdings {
		if h.GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, h.InstrumentId)
			if err == nil && inst != nil {
				h.Instrument = instrumentRowToProto(inst)
			}
		}
	}
	return &apiv1.GetHoldingsResponse{Holdings: holdings, AsOf: asOf}, nil
}

// ExportInstruments streams instruments that have at least one canonical identifier. Optional exchange filter.
// TODO(admin): restrict to admin once roles are enforced.
func (s *Server) ExportInstruments(req *apiv1.ExportInstrumentsRequest, stream apiv1.ApiService_ExportInstrumentsServer) error {
	ctx := stream.Context()
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return status.Error(codes.Unauthenticated, "missing user")
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange())
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, row := range rows {
		if err := stream.Send(instrumentRowToProto(row)); err != nil {
			return err
		}
	}
	return nil
}

// ImportInstruments ensures the given instruments exist (find-or-create by identifiers). No overwrite of canonical fields for existing.
// TODO(admin): restrict to admin once roles are enforced.
func (s *Server) ImportInstruments(ctx context.Context, req *apiv1.ImportInstrumentsRequest) (*apiv1.ImportInstrumentsResponse, error) {
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	var errs []*apiv1.ImportInstrumentError
	seenKeys := make(map[string]struct{})
	var ensuredCount int32
	for i, inst := range req.GetInstruments() {
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
		_, err := s.db.EnsureInstrument(ctx, inst.GetAssetClass(), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), idns)
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensuredCount, Errors: errs}, nil
}

// GetJob returns ingestion job status and validation errors; job's portfolio must belong to user.
func (s *Server) GetJob(ctx context.Context, req *apiv1.GetJobRequest) (*apiv1.GetJobResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id required")
	}
	statusVal, errs, idErrs, portfolioID, err := s.db.GetJob(ctx, req.GetJobId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if portfolioID == "" {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, portfolioID, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	idErrProtos := make([]*apiv1.IdentificationError, 0, len(idErrs))
	for _, e := range idErrs {
		idErrProtos = append(idErrProtos, &apiv1.IdentificationError{
			RowIndex:               e.RowIndex,
			InstrumentDescription: e.InstrumentDescription,
			Message:                e.Message,
		})
	}
	return &apiv1.GetJobResponse{Status: statusVal, ValidationErrors: errs, IdentificationErrors: idErrProtos}, nil
}

func instrumentRowToProto(row *db.InstrumentRow) *apiv1.Instrument {
	if row == nil {
		return nil
	}
	identifiers := make([]*apiv1.InstrumentIdentifier, 0, len(row.Identifiers))
	for _, idn := range row.Identifiers {
		identifiers = append(identifiers, &apiv1.InstrumentIdentifier{Type: idn.Type, Value: idn.Value, Canonical: idn.Canonical})
	}
	return &apiv1.Instrument{
		Id:          row.ID,
		AssetClass:  row.AssetClass,
		Exchange:    row.Exchange,
		Currency:    row.Currency,
		Name:        row.Name,
		Identifiers: identifiers,
	}
}
