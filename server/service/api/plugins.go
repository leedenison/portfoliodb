package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/leedenison/portfoliodb/server/auth"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListPricePlugins returns all price plugin configs. Admin only.
func (s *Server) ListPricePlugins(ctx context.Context, req *apiv1.ListPricePluginsRequest) (*apiv1.ListPricePluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListPricePluginConfigs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	plugins := make([]*apiv1.PricePluginConfig, 0, len(rows))
	for _, r := range rows {
		configJSON := ""
		if len(r.Config) > 0 {
			configJSON = string(r.Config)
		}
		displayName := r.PluginID
		if s.priceRegistry != nil {
			displayName = s.priceRegistry.GetDisplayName(r.PluginID)
		}
		plugins = append(plugins, &apiv1.PricePluginConfig{
			PluginId:    r.PluginID,
			Enabled:     r.Enabled,
			Precedence:  int32(r.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		})
	}
	return &apiv1.ListPricePluginsResponse{Plugins: plugins}, nil
}

// UpdatePricePlugin updates enabled, precedence, and/or config for a price plugin. Admin only.
func (s *Server) UpdatePricePlugin(ctx context.Context, req *apiv1.UpdatePricePluginRequest) (*apiv1.UpdatePricePluginResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
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
	row, err := s.db.UpdatePricePluginConfig(ctx, req.GetPluginId(), enabled, precedence, config, nil)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "plugin not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	configJSON := ""
	if len(row.Config) > 0 {
		configJSON = string(row.Config)
	}
	displayName := row.PluginID
	if s.priceRegistry != nil {
		displayName = s.priceRegistry.GetDisplayName(row.PluginID)
	}
	return &apiv1.UpdatePricePluginResponse{
		Plugin: &apiv1.PricePluginConfig{
			PluginId:    row.PluginID,
			Enabled:     row.Enabled,
			Precedence:  int32(row.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		},
	}, nil
}

// TriggerPriceFetch signals the price fetcher worker to run a cycle. Admin only.
// Returns immediately; the fetch runs asynchronously.
func (s *Server) TriggerPriceFetch(ctx context.Context, req *apiv1.TriggerPriceFetchRequest) (*apiv1.TriggerPriceFetchResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if s.priceTrigger != nil {
		select {
		case s.priceTrigger <- struct{}{}:
		default:
		}
	}
	return &apiv1.TriggerPriceFetchResponse{}, nil
}

// ListIdentifierPlugins returns all identifier plugin configs. Admin only.
func (s *Server) ListIdentifierPlugins(ctx context.Context, req *apiv1.ListIdentifierPluginsRequest) (*apiv1.ListIdentifierPluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
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
		displayName := r.PluginID
		if s.pluginRegistry != nil {
			displayName = s.pluginRegistry.GetDisplayName(r.PluginID)
		}
		plugins = append(plugins, &apiv1.IdentifierPluginConfig{
			PluginId:    r.PluginID,
			Enabled:     r.Enabled,
			Precedence:  int32(r.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		})
	}
	return &apiv1.ListIdentifierPluginsResponse{Plugins: plugins}, nil
}

// UpdateIdentifierPlugin updates enabled, precedence, and/or config for a plugin. Admin only.
func (s *Server) UpdateIdentifierPlugin(ctx context.Context, req *apiv1.UpdateIdentifierPluginRequest) (*apiv1.UpdateIdentifierPluginResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "plugin not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	configJSON := ""
	if len(row.Config) > 0 {
		configJSON = string(row.Config)
	}
	displayName := row.PluginID
	if s.pluginRegistry != nil {
		displayName = s.pluginRegistry.GetDisplayName(row.PluginID)
	}
	return &apiv1.UpdateIdentifierPluginResponse{
		Plugin: &apiv1.IdentifierPluginConfig{
			PluginId:    row.PluginID,
			Enabled:     row.Enabled,
			Precedence:  int32(row.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		},
	}, nil
}

// ListDescriptionPlugins returns all description plugin configs. Admin only.
func (s *Server) ListDescriptionPlugins(ctx context.Context, req *apiv1.ListDescriptionPluginsRequest) (*apiv1.ListDescriptionPluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListDescriptionPluginConfigs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	plugins := make([]*apiv1.DescriptionPluginConfig, 0, len(rows))
	for _, r := range rows {
		configJSON := ""
		if len(r.Config) > 0 {
			configJSON = string(r.Config)
		}
		displayName := r.PluginID
		if s.descRegistry != nil {
			displayName = s.descRegistry.GetDisplayName(r.PluginID)
		}
		plugins = append(plugins, &apiv1.DescriptionPluginConfig{
			PluginId:    r.PluginID,
			Enabled:     r.Enabled,
			Precedence:  int32(r.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		})
	}
	return &apiv1.ListDescriptionPluginsResponse{Plugins: plugins}, nil
}

// UpdateDescriptionPlugin updates enabled, precedence, and/or config for a description plugin. Admin only.
func (s *Server) UpdateDescriptionPlugin(ctx context.Context, req *apiv1.UpdateDescriptionPluginRequest) (*apiv1.UpdateDescriptionPluginResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
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
	row, err := s.db.UpdateDescriptionPluginConfig(ctx, req.GetPluginId(), enabled, precedence, config)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Error(codes.NotFound, "plugin not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	configJSON := ""
	if len(row.Config) > 0 {
		configJSON = string(row.Config)
	}
	displayName := row.PluginID
	if s.descRegistry != nil {
		displayName = s.descRegistry.GetDisplayName(row.PluginID)
	}
	return &apiv1.UpdateDescriptionPluginResponse{
		Plugin: &apiv1.DescriptionPluginConfig{
			PluginId:    row.PluginID,
			Enabled:     row.Enabled,
			Precedence:  int32(row.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		},
	}, nil
}
