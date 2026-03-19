package api

import (
	"context"
	"database/sql"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

func TestListPricePlugins_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.ListPricePlugins(ctx, &apiv1.ListPricePluginsRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestListPricePlugins_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	srv := NewServer(ServerConfig{DB: mockDB})

	mockDB.EXPECT().ListPluginConfigs(gomock.Any(), db.PluginCategoryPrice).Return([]db.PluginConfigRowFull{
		{PluginID: "massive", Enabled: true, Precedence: 10, Config: []byte(`{"key":"val"}`)},
	}, nil)

	ctx := adminCtx("admin-1", "sub|admin")
	resp, err := srv.ListPricePlugins(ctx, &apiv1.ListPricePluginsRequest{})
	if err != nil {
		t.Fatalf("ListPricePlugins: %v", err)
	}
	if len(resp.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(resp.Plugins))
	}
	p := resp.Plugins[0]
	if p.PluginId != "massive" || !p.Enabled || p.Precedence != 10 {
		t.Errorf("unexpected plugin: %+v", p)
	}
}

func TestUpdatePricePlugin_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{PluginId: "x"})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestUpdatePricePlugin_MissingPluginID(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("admin-1", "sub|admin")
	_, err := srv.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestUpdatePricePlugin_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	srv := NewServer(ServerConfig{DB: mockDB})

	mockDB.EXPECT().UpdatePluginConfig(gomock.Any(), db.PluginCategoryPrice, "missing", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, sql.ErrNoRows)

	ctx := adminCtx("admin-1", "sub|admin")
	enabled := true
	_, err := srv.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{
		PluginId: "missing",
		Enabled:  &enabled,
	})
	testutil.RequireGRPCCode(t, err, codes.NotFound)
}

func TestUpdatePricePlugin_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	srv := NewServer(ServerConfig{DB: mockDB})

	mockDB.EXPECT().UpdatePluginConfig(gomock.Any(), db.PluginCategoryPrice, "massive", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&db.PluginConfigRowFull{PluginID: "massive", Enabled: true, Precedence: 10, Config: []byte("{}")}, nil)

	ctx := adminCtx("admin-1", "sub|admin")
	enabled := true
	resp, err := srv.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{
		PluginId: "massive",
		Enabled:  &enabled,
	})
	if err != nil {
		t.Fatalf("UpdatePricePlugin: %v", err)
	}
	if !resp.Plugin.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestTriggerPriceFetch_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := authCtx("user-1", "sub|1")
	_, err := srv.TriggerPriceFetch(ctx, &apiv1.TriggerPriceFetchRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestTriggerPriceFetch_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	srv := NewServer(ServerConfig{DB: mockDB, PriceTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	_, err := srv.TriggerPriceFetch(ctx, &apiv1.TriggerPriceFetchRequest{})
	if err != nil {
		t.Fatalf("TriggerPriceFetch: %v", err)
	}
	select {
	case <-trigger:
		// ok
	default:
		t.Error("expected signal on trigger channel")
	}
}

func TestTriggerPriceFetch_NilTrigger(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := adminCtx("admin-1", "sub|admin")
	_, err := srv.TriggerPriceFetch(ctx, &apiv1.TriggerPriceFetchRequest{})
	if err != nil {
		t.Fatalf("TriggerPriceFetch with nil trigger should succeed: %v", err)
	}
}

func TestTriggerPriceFetch_NonBlocking(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(func() { ctrl.Finish() })
	mockDB := mock.NewMockDB(ctrl)
	trigger := make(chan struct{}, 1)
	trigger <- struct{}{} // pre-fill
	srv := NewServer(ServerConfig{DB: mockDB, PriceTrigger: trigger})

	ctx := adminCtx("admin-1", "sub|admin")
	// Should not block even though channel is full.
	_, err := srv.TriggerPriceFetch(ctx, &apiv1.TriggerPriceFetchRequest{})
	if err != nil {
		t.Fatalf("TriggerPriceFetch should not block: %v", err)
	}
}

// Unauthenticated tests for new admin endpoints.
func TestAPI_PricePlugins_Unauthenticated(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"ListPricePlugins", func() error { _, err := srv.ListPricePlugins(ctx, &apiv1.ListPricePluginsRequest{}); return err }},
		{"UpdatePricePlugin", func() error {
			_, err := srv.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{PluginId: "x"})
			return err
		}},
		{"TriggerPriceFetch", func() error { _, err := srv.TriggerPriceFetch(ctx, &apiv1.TriggerPriceFetchRequest{}); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.RequireGRPCCode(t, tc.call(), codes.Unauthenticated)
		})
	}
}
