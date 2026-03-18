package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGetPricePluginConfig_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	row, err := p.GetPricePluginConfig(ctx, "nonexistent-price-plugin")
	if err == nil {
		t.Fatalf("expected error, got row %+v", row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
	if row != nil {
		t.Errorf("expected nil row when not found, got %+v", row)
	}
}

func TestInsertPricePluginConfig_GetPricePluginConfig(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	pluginID := "test-price-plugin"
	config := []byte(`{"massive_api_key":"","massive_calls_per_min":5}`)
	inserted, err := p.InsertPricePluginConfig(ctx, pluginID, false, 10, config, nil)
	if err != nil {
		t.Fatalf("InsertPricePluginConfig: %v", err)
	}
	if inserted == nil {
		t.Fatal("InsertPricePluginConfig returned nil row")
	}
	if inserted.PluginID != pluginID {
		t.Errorf("PluginID = %q, want %q", inserted.PluginID, pluginID)
	}
	if inserted.Enabled != false {
		t.Errorf("Enabled = %v, want false", inserted.Enabled)
	}
	if inserted.Precedence != 10 {
		t.Errorf("Precedence = %d, want 10", inserted.Precedence)
	}
	if !jsonEqual(inserted.Config, config) {
		wantV, _ := decodeJSON(config)
		gotV, _ := decodeJSON(inserted.Config)
		t.Errorf("Config:\n%s", cmp.Diff(wantV, gotV))
	}
	got, err := p.GetPricePluginConfig(ctx, pluginID)
	if err != nil {
		t.Fatalf("GetPricePluginConfig: %v", err)
	}
	if got.PluginID != pluginID || got.Enabled != inserted.Enabled || got.Precedence != inserted.Precedence {
		t.Errorf("GetPricePluginConfig: got %+v, want same as inserted %+v", got, inserted)
	}
	if !jsonEqual(got.Config, config) {
		wantV, _ := decodeJSON(config)
		gotV, _ := decodeJSON(got.Config)
		t.Errorf("GetPricePluginConfig config:\n%s", cmp.Diff(wantV, gotV))
	}
}

func TestInsertPricePluginConfig_EmptyConfigStoredAsEmptyObject(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inserted, err := p.InsertPricePluginConfig(ctx, "empty-price-config", true, 20, nil, nil)
	if err != nil {
		t.Fatalf("InsertPricePluginConfig: %v", err)
	}
	if string(inserted.Config) != "{}" {
		t.Errorf("nil config should be stored as {}, got %q", inserted.Config)
	}
}

func TestInsertPricePluginConfig_DuplicateRejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.InsertPricePluginConfig(ctx, "dup-price", false, 10, []byte("{}"), nil)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = p.InsertPricePluginConfig(ctx, "dup-price", true, 20, []byte(`{"x":1}`), nil)
	if err == nil {
		t.Fatal("second insert with same plugin_id should fail")
	}
}

func TestUpdatePricePluginConfig(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.InsertPricePluginConfig(ctx, "upd-price", false, 10, []byte(`{"key":"old"}`), nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	enabled := true
	prec := 50
	row, err := p.UpdatePricePluginConfig(ctx, "upd-price", &enabled, &prec, []byte(`{"key":"new"}`), nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !row.Enabled {
		t.Error("expected enabled=true after update")
	}
	if row.Precedence != 50 {
		t.Errorf("expected precedence=50, got %d", row.Precedence)
	}
	if !jsonEqual(row.Config, []byte(`{"key":"new"}`)) {
		t.Errorf("config not updated: %s", row.Config)
	}
}

func TestListEnabledPricePluginConfigs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, _ = p.InsertPricePluginConfig(ctx, "enabled-price", true, 20, []byte("{}"), nil)
	_, _ = p.InsertPricePluginConfig(ctx, "disabled-price", false, 10, []byte("{}"), nil)

	rows, err := p.ListEnabledPricePluginConfigs(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 enabled plugin, got %d", len(rows))
	}
	if rows[0].PluginID != "enabled-price" {
		t.Errorf("expected enabled-price, got %s", rows[0].PluginID)
	}
}

func TestListPricePluginConfigs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, _ = p.InsertPricePluginConfig(ctx, "price-a", true, 20, []byte("{}"), nil)
	_, _ = p.InsertPricePluginConfig(ctx, "price-b", false, 10, []byte("{}"), nil)

	rows, err := p.ListPricePluginConfigs(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(rows))
	}
	// Sorted by precedence DESC: 20, 10
	if rows[0].PluginID != "price-a" || rows[1].PluginID != "price-b" {
		t.Errorf("unexpected order: %s, %s", rows[0].PluginID, rows[1].PluginID)
	}
}
