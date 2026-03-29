package api

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetIgnoredAssetClasses returns the authenticated user's ignored asset class rules.
func (s *Server) GetIgnoredAssetClasses(ctx context.Context, _ *apiv1.GetIgnoredAssetClassesRequest) (*apiv1.GetIgnoredAssetClassesResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	rules, err := s.db.ListIgnoredAssetClasses(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.GetIgnoredAssetClassesResponse{Rules: rulesToProto(rules)}, nil
}

// SetIgnoredAssetClasses replaces the user's ignored asset class rules and deletes matching txs.
func (s *Server) SetIgnoredAssetClasses(ctx context.Context, req *apiv1.SetIgnoredAssetClassesRequest) (*apiv1.SetIgnoredAssetClassesResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	rules, err := rulesFromProto(req.GetRules())
	if err != nil {
		return nil, err
	}
	mapping := buildAssetClassToTxTypesMap(rules)
	if err := s.db.SetIgnoredAssetClasses(ctx, u.ID, rules, mapping); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.SetIgnoredAssetClassesResponse{}, nil
}

// CountIgnoredTxs returns the count of txs and declarations that would be deleted.
func (s *Server) CountIgnoredTxs(ctx context.Context, req *apiv1.CountIgnoredTxsRequest) (*apiv1.CountIgnoredTxsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	rules, err := rulesFromProto(req.GetRules())
	if err != nil {
		return nil, err
	}
	mapping := buildAssetClassToTxTypesMap(rules)
	txCount, declCount, err := s.db.CountIgnoredTxs(ctx, u.ID, rules, mapping)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.CountIgnoredTxsResponse{TxCount: txCount, DeclarationCount: declCount}, nil
}

func rulesToProto(rules []db.IgnoredAssetClass) []*apiv1.IgnoredAssetClassRule {
	out := make([]*apiv1.IgnoredAssetClassRule, len(rules))
	for i, r := range rules {
		out[i] = &apiv1.IgnoredAssetClassRule{
			Broker:     r.Broker,
			Account:    r.Account,
			AssetClass: r.AssetClass,
		}
	}
	return out
}

func rulesFromProto(protos []*apiv1.IgnoredAssetClassRule) ([]db.IgnoredAssetClass, error) {
	rules := make([]db.IgnoredAssetClass, len(protos))
	for i, p := range protos {
		if p.GetBroker() == "" {
			return nil, status.Error(codes.InvalidArgument, "broker is required")
		}
		ac := p.GetAssetClass()
		if !db.ValidAssetClasses[ac] {
			return nil, status.Errorf(codes.InvalidArgument, "invalid asset_class: %s", ac)
		}
		rules[i] = db.IgnoredAssetClass{
			Broker:     p.GetBroker(),
			Account:    p.GetAccount(),
			AssetClass: ac,
		}
	}
	return rules, nil
}

// buildAssetClassToTxTypesMap builds the reverse mapping for the asset classes present in the rules.
func buildAssetClassToTxTypesMap(rules []db.IgnoredAssetClass) map[string][]string {
	seen := make(map[string]bool)
	mapping := make(map[string][]string)
	for _, r := range rules {
		if seen[r.AssetClass] {
			continue
		}
		seen[r.AssetClass] = true
		mapping[r.AssetClass] = db.AssetClassToTxTypeStrings(r.AssetClass)
	}
	return mapping
}
