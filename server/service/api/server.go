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
	return &apiv1.GetHoldingsResponse{Holdings: holdings, AsOf: asOf}, nil
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
	statusVal, errs, portfolioID, err := s.db.GetJob(ctx, req.GetJobId())
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
	return &apiv1.GetJobResponse{Status: statusVal, ValidationErrors: errs}, nil
}
