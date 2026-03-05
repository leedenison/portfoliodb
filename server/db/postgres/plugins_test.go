package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
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
	row, err := p.GetPluginConfig(ctx, "nonexistent-plugin")
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
	inserted, err := p.InsertPluginConfig(ctx, pluginID, false, 10, config)
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
	got, err := p.GetPluginConfig(ctx, pluginID)
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
	inserted, err := p.InsertPluginConfig(ctx, pluginID, true, 20, nil)
	if err != nil {
		t.Fatalf("InsertPluginConfig: %v", err)
	}
	if inserted == nil {
		t.Fatal("InsertPluginConfig returned nil row")
	}
	if string(inserted.Config) != "{}" {
		t.Errorf("nil config should be stored as {}, got %q", inserted.Config)
	}
	got, err := p.GetPluginConfig(ctx, pluginID)
	if err != nil {
		t.Fatalf("GetPluginConfig: %v", err)
	}
	if string(got.Config) != "{}" {
		t.Errorf("GetPluginConfig config = %q, want {}", got.Config)
	}
	// Empty slice also becomes {}
	inserted2, err := p.InsertPluginConfig(ctx, "empty-slice-plugin", false, 30, []byte{})
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
	_, err := p.InsertPluginConfig(ctx, pluginID, false, 10, []byte("{}"))
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = p.InsertPluginConfig(ctx, pluginID, true, 20, []byte(`{"x":1}`))
	if err == nil {
		t.Fatal("second insert with same plugin_id should fail")
	}
}
