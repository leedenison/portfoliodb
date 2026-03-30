package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReorderPlugins sets the precedence order for all plugins in a category. Admin only.
func (s *Server) ReorderPlugins(ctx context.Context, req *apiv1.ReorderPluginsRequest) (*apiv1.ReorderPluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	cat := req.GetCategory()
	if cat != db.PluginCategoryIdentifier && cat != db.PluginCategoryDescription && cat != db.PluginCategoryPrice {
		return nil, status.Error(codes.InvalidArgument, "category must be identifier, description, or price")
	}
	if len(req.GetPluginIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "plugin_ids required")
	}
	existing, err := s.db.ListPluginConfigs(ctx, cat)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if len(req.GetPluginIds()) != len(existing) {
		return nil, status.Error(codes.InvalidArgument, "plugin_ids must contain all plugins in the category")
	}
	have := make(map[string]bool, len(existing))
	for _, r := range existing {
		have[r.PluginID] = true
	}
	for _, pid := range req.GetPluginIds() {
		if !have[pid] {
			return nil, status.Errorf(codes.InvalidArgument, "unknown plugin_id: %s", pid)
		}
	}
	if err := s.db.ReorderPluginConfigs(ctx, cat, req.GetPluginIds()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.ReorderPluginsResponse{}, nil
}

// ListPricePlugins returns all price plugin configs. Admin only.
func (s *Server) ListPricePlugins(ctx context.Context, req *apiv1.ListPricePluginsRequest) (*apiv1.ListPricePluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListPluginConfigs(ctx, db.PluginCategoryPrice)
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
		cfg := &apiv1.PricePluginConfig{
			PluginId:    r.PluginID,
			Enabled:     r.Enabled,
			Precedence:  int32(r.Precedence),
			ConfigJson:  configJSON,
			DisplayName: displayName,
		}
		if r.MaxHistoryDays != nil {
			v := int32(*r.MaxHistoryDays)
			cfg.MaxHistoryDays = &v
		}
		plugins = append(plugins, cfg)
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
	var maxHistDays *int
	if req.MaxHistoryDays != nil {
		v := int(*req.MaxHistoryDays)
		maxHistDays = &v
	}
	row, err := s.db.UpdatePluginConfig(ctx, db.PluginCategoryPrice, req.GetPluginId(), enabled, precedence, config, maxHistDays)
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
	respCfg := &apiv1.PricePluginConfig{
		PluginId:    row.PluginID,
		Enabled:     row.Enabled,
		Precedence:  int32(row.Precedence),
		ConfigJson:  configJSON,
		DisplayName: displayName,
	}
	if row.MaxHistoryDays != nil {
		v := int32(*row.MaxHistoryDays)
		respCfg.MaxHistoryDays = &v
	}
	return &apiv1.UpdatePricePluginResponse{Plugin: respCfg}, nil
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

// ListPriceFetchBlocks returns all blocked (instrument, plugin) pairs. Admin only.
func (s *Server) ListPriceFetchBlocks(ctx context.Context, req *apiv1.ListPriceFetchBlocksRequest) (*apiv1.ListPriceFetchBlocksResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	blocks, err := s.db.ListPriceFetchBlocks(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Collect instrument IDs for display name enrichment.
	idSet := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		idSet[b.InstrumentID] = true
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	instMap := make(map[string]string) // instrumentID -> display name
	if len(ids) > 0 {
		instruments, err := s.db.ListInstrumentsByIDs(ctx, ids)
		if err == nil {
			for _, inst := range instruments {
				name := inst.ID
				if inst.Name != nil && *inst.Name != "" {
					name = *inst.Name
				}
				instMap[inst.ID] = name
			}
		}
	}
	pbBlocks := make([]*apiv1.PriceFetchBlock, 0, len(blocks))
	for _, b := range blocks {
		pluginName := b.PluginID
		if s.priceRegistry != nil {
			pluginName = s.priceRegistry.GetDisplayName(b.PluginID)
		}
		instName := instMap[b.InstrumentID]
		if instName == "" {
			instName = b.InstrumentID
		}
		pbBlocks = append(pbBlocks, &apiv1.PriceFetchBlock{
			InstrumentId:          b.InstrumentID,
			PluginId:              b.PluginID,
			Reason:                b.Reason,
			CreatedAt:             timestamppb.New(b.CreatedAt),
			PluginDisplayName:     pluginName,
			InstrumentDisplayName: instName,
		})
	}
	return &apiv1.ListPriceFetchBlocksResponse{Blocks: pbBlocks}, nil
}

// DeletePriceFetchBlock removes a block for an (instrument, plugin) pair. Admin only.
func (s *Server) DeletePriceFetchBlock(ctx context.Context, req *apiv1.DeletePriceFetchBlockRequest) (*apiv1.DeletePriceFetchBlockResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	if req.GetInstrumentId() == "" || req.GetPluginId() == "" {
		return nil, status.Error(codes.InvalidArgument, "instrument_id and plugin_id required")
	}
	if err := s.db.DeletePriceFetchBlock(ctx, req.GetInstrumentId(), req.GetPluginId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.DeletePriceFetchBlockResponse{}, nil
}

// ListIdentifierPlugins returns all identifier plugin configs. Admin only.
func (s *Server) ListIdentifierPlugins(ctx context.Context, req *apiv1.ListIdentifierPluginsRequest) (*apiv1.ListIdentifierPluginsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListPluginConfigs(ctx, db.PluginCategoryIdentifier)
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
	row, err := s.db.UpdatePluginConfig(ctx, db.PluginCategoryIdentifier, req.GetPluginId(), enabled, precedence, config, nil)
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
	rows, err := s.db.ListPluginConfigs(ctx, db.PluginCategoryDescription)
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
	row, err := s.db.UpdatePluginConfig(ctx, db.PluginCategoryDescription, req.GetPluginId(), enabled, precedence, config, nil)
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
