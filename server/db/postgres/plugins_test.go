package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/leedenison/portfoliodb/server/db"
)

// jsonEqual compares two JSON byte slices for semantic equality (Postgres JSONB may return different whitespace).
func jsonEqual(a, b []byte) bool {
	av, errA := decodeJSON(a)
	bv, errB := decodeJSON(b)
	if errA != nil || errB != nil {
		return false
	}
	return cmp.Equal(av, bv)
}

func decodeJSON(b []byte) (interface{}, error) {
	var v interface{}
	err := json.Unmarshal(b, &v)
	return v, err
}

func TestGetPluginConfig_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	row, err := p.GetPluginConfig(ctx, db.PluginCategoryIdentifier, "nonexistent-plugin")
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

func TestInsertPluginConfig_GetPluginConfig(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	pluginID := "test-plugin"
	config := []byte(`{"api_key":"","enabled":false}`)
	inserted, err := p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID, false, 10, config, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig: %v", err)
	}
	if inserted == nil {
		t.Fatal("InsertPluginConfig returned nil row")
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
	// GetPluginConfig should return the same row (JSONB may return different whitespace)
	got, err := p.GetPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID)
	if err != nil {
		t.Fatalf("GetPluginConfig: %v", err)
	}
	if got.PluginID != pluginID || got.Enabled != inserted.Enabled || got.Precedence != inserted.Precedence {
		t.Errorf("GetPluginConfig: got %+v, want same as inserted %+v", got, inserted)
	}
	if !jsonEqual(got.Config, config) {
		wantV, _ := decodeJSON(config)
		gotV, _ := decodeJSON(got.Config)
		t.Errorf("GetPluginConfig config:\n%s", cmp.Diff(wantV, gotV))
	}
}

func TestInsertPluginConfig_EmptyConfigStoredAsEmptyObject(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	pluginID := "empty-config-plugin"
	inserted, err := p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID, true, 20, nil, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig: %v", err)
	}
	if inserted == nil {
		t.Fatal("InsertPluginConfig returned nil row")
	}
	if string(inserted.Config) != "{}" {
		t.Errorf("nil config should be stored as {}, got %q", inserted.Config)
	}
	got, err := p.GetPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID)
	if err != nil {
		t.Fatalf("GetPluginConfig: %v", err)
	}
	if string(got.Config) != "{}" {
		t.Errorf("GetPluginConfig config = %q, want {}", got.Config)
	}
	// Empty slice also becomes {}
	inserted2, err := p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, "empty-slice-plugin", false, 30, []byte{}, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig empty slice: %v", err)
	}
	if string(inserted2.Config) != "{}" {
		t.Errorf("empty slice config should be stored as {}, got %q", inserted2.Config)
	}
}

func TestInsertPluginConfig_DuplicatePluginID_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	pluginID := "dup-plugin"
	_, err := p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID, false, 10, []byte("{}"), nil)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID, true, 20, []byte(`{"x":1}`), nil)
	if err == nil {
		t.Fatal("second insert with same (plugin_id, category) should fail")
	}
}

func TestInsertPluginConfig_SamePluginDifferentCategory_Allowed(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	pluginID := "cash"
	_, err := p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, pluginID, true, 10, []byte("{}"), nil)
	if err != nil {
		t.Fatalf("insert identifier: %v", err)
	}
	_, err = p.InsertPluginConfig(ctx, db.PluginCategoryDescription, pluginID, true, 10, []byte("{}"), nil)
	if err != nil {
		t.Fatalf("insert description (same plugin_id, different category): %v", err)
	}
}

func TestGetPricePluginConfig_NotFound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	row, err := p.GetPluginConfig(ctx, db.PluginCategoryPrice, "nonexistent-price-plugin")
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
	inserted, err := p.InsertPluginConfig(ctx, db.PluginCategoryPrice, pluginID, false, 10, config, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig: %v", err)
	}
	if inserted == nil {
		t.Fatal("InsertPluginConfig returned nil row")
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
	got, err := p.GetPluginConfig(ctx, db.PluginCategoryPrice, pluginID)
	if err != nil {
		t.Fatalf("GetPluginConfig: %v", err)
	}
	if got.PluginID != pluginID || got.Enabled != inserted.Enabled || got.Precedence != inserted.Precedence {
		t.Errorf("GetPluginConfig: got %+v, want same as inserted %+v", got, inserted)
	}
	if !jsonEqual(got.Config, config) {
		wantV, _ := decodeJSON(config)
		gotV, _ := decodeJSON(got.Config)
		t.Errorf("GetPluginConfig config:\n%s", cmp.Diff(wantV, gotV))
	}
}

func TestInsertPricePluginConfig_EmptyConfigStoredAsEmptyObject(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inserted, err := p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "empty-price-config", true, 20, nil, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig: %v", err)
	}
	if string(inserted.Config) != "{}" {
		t.Errorf("nil config should be stored as {}, got %q", inserted.Config)
	}
}

func TestInsertPricePluginConfig_DuplicateRejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "dup-price", false, 10, []byte("{}"), nil)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "dup-price", true, 20, []byte(`{"x":1}`), nil)
	if err == nil {
		t.Fatal("second insert with same (plugin_id, category) should fail")
	}
}

func TestUpdatePricePluginConfig(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "upd-price", false, 10, []byte(`{"key":"old"}`), nil)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	enabled := true
	prec := 50
	row, err := p.UpdatePluginConfig(ctx, db.PluginCategoryPrice, "upd-price", &enabled, &prec, []byte(`{"key":"new"}`), nil)
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
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "enabled-price", true, 20, []byte("{}"), nil)
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "disabled-price", false, 10, []byte("{}"), nil)

	rows, err := p.ListEnabledPluginConfigs(ctx, db.PluginCategoryPrice)
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
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "price-a", true, 20, []byte("{}"), nil)
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "price-b", false, 10, []byte("{}"), nil)

	rows, err := p.ListPluginConfigs(ctx, db.PluginCategoryPrice)
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

func TestListEnabledPluginConfigs_CategoryIsolation(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryIdentifier, "id-plugin", true, 10, []byte("{}"), nil)
	_, _ = p.InsertPluginConfig(ctx, db.PluginCategoryPrice, "price-plugin", true, 10, []byte("{}"), nil)

	idRows, err := p.ListEnabledPluginConfigs(ctx, db.PluginCategoryIdentifier)
	if err != nil {
		t.Fatalf("list identifier: %v", err)
	}
	if len(idRows) != 1 || idRows[0].PluginID != "id-plugin" {
		t.Errorf("expected only id-plugin, got %+v", idRows)
	}

	priceRows, err := p.ListEnabledPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		t.Fatalf("list price: %v", err)
	}
	if len(priceRows) != 1 || priceRows[0].PluginID != "price-plugin" {
		t.Errorf("expected only price-plugin, got %+v", priceRows)
	}
}
