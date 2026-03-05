package api

import (
	"context"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// GetPortfolioFilters returns the filter rows for a portfolio (broker/account/instrument). Portfolio must be owned by user.
func (s *Server) GetPortfolioFilters(ctx context.Context, req *apiv1.GetPortfolioFiltersRequest) (*apiv1.GetPortfolioFiltersResponse, error) {
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
	filters, err := s.db.ListPortfolioFilters(ctx, req.GetPortfolioId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	protos := make([]*apiv1.PortfolioFilterProto, 0, len(filters))
	for _, f := range filters {
		protos = append(protos, &apiv1.PortfolioFilterProto{FilterType: f.FilterType, FilterValue: f.FilterValue})
	}
	return &apiv1.GetPortfolioFiltersResponse{Filters: protos}, nil
}

// SetPortfolioFilters replaces all filters for a portfolio. Portfolio must be owned by user.
func (s *Server) SetPortfolioFilters(ctx context.Context, req *apiv1.SetPortfolioFiltersRequest) (*apiv1.SetPortfolioFiltersResponse, error) {
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
	filters := make([]db.PortfolioFilter, 0, len(req.GetFilters()))
	for _, f := range req.GetFilters() {
		if f.GetFilterType() != "" {
			filters = append(filters, db.PortfolioFilter{FilterType: f.GetFilterType(), FilterValue: f.GetFilterValue()})
		}
	}
	if err := s.db.SetPortfolioFilters(ctx, req.GetPortfolioId(), filters); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.SetPortfolioFiltersResponse{}, nil
}
