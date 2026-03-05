package api

import (
	"context"
	"strings"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListIdentifierPlugins returns all identifier plugin configs. Admin only.
func (s *Server) ListIdentifierPlugins(ctx context.Context, req *apiv1.ListIdentifierPluginsRequest) (*apiv1.ListIdentifierPluginsResponse, error) {
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	rows, err := s.db.ListPluginConfigs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	plugins := make([]*apiv1.IdentifierPluginConfig, 0, len(rows))
	for _, r := range rows {
		configJSON := ""
		if len(r.Config) > 0 {
			configJSON = string(r.Config)
		}
		plugins = append(plugins, &apiv1.IdentifierPluginConfig{
			PluginId:   r.PluginID,
			Enabled:    r.Enabled,
			Precedence: int32(r.Precedence),
			ConfigJson: configJSON,
		})
	}
	return &apiv1.ListIdentifierPluginsResponse{Plugins: plugins}, nil
}

// UpdateIdentifierPlugin updates enabled, precedence, and/or config for a plugin. Admin only.
func (s *Server) UpdateIdentifierPlugin(ctx context.Context, req *apiv1.UpdateIdentifierPluginRequest) (*apiv1.UpdateIdentifierPluginResponse, error) {
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	if req.GetPluginId() == "" {
		return nil, status.Error(codes.InvalidArgument, "plugin_id required")
	}
	var enabled *bool
	if req.Enabled != nil {
		enabled = req.Enabled
	}
	var precedence *int
	if req.Precedence != nil {
		p := int(*req.Precedence)
		precedence = &p
	}
	var config []byte
	if req.ConfigJson != nil {
		config = []byte(*req.ConfigJson)
	}
	row, err := s.db.UpdatePluginConfig(ctx, req.GetPluginId(), enabled, precedence, config)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Error(codes.NotFound, "plugin not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	configJSON := ""
	if len(row.Config) > 0 {
		configJSON = string(row.Config)
	}
	return &apiv1.UpdateIdentifierPluginResponse{
		Plugin: &apiv1.IdentifierPluginConfig{
			PluginId:   row.PluginID,
			Enabled:    row.Enabled,
			Precedence: int32(row.Precedence),
			ConfigJson: configJSON,
		},
	}, nil
}
