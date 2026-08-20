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
	_, err = p.InsertPluginConfig(ctx, db.PluginCategoryCandidate, pluginID, true, 10, []byte("{}"), nil)
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

// seedPluginConfigs inserts rows the way the service does at startup: one per
// registered plugin, disabled, with spaced-out precedences.
func seedPluginConfigs(t *testing.T, p *Postgres, category string, pluginIDs ...string) {
	t.Helper()
	ctx := context.Background()
	for i, id := range pluginIDs {
		if _, err := p.InsertPluginConfig(ctx, category, id, false, 10*(i+1), []byte(`{}`), nil); err != nil {
			t.Fatalf("seed %s/%s: %v", category, id, err)
		}
	}
}

func TestListAllPluginConfigs(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd", "massive")
	seedPluginConfigs(t, p, db.PluginCategoryInflation, "ons")

	rows, err := p.ListAllPluginConfigs(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// Ordered by category, then precedence descending.
	if rows[0].Category != db.PluginCategoryInflation || rows[0].PluginID != "ons" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].PluginID != "massive" || rows[2].PluginID != "eodhd" {
		t.Fatalf("price order = %s, %s", rows[1].PluginID, rows[2].PluginID)
	}
}

// The ordinary case: the file names every plugin in the category, so the stored
// precedences come out exactly as the file stated them even though the
// instance's own values were in the way.
func TestRestorePluginConfigs_AppliesTheFilesPrecedencesExactly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd", "massive")

	if err := p.RestorePluginConfigs(ctx, []db.PluginConfigWithCategory{
		{PluginID: "eodhd", Category: db.PluginCategoryPrice, Enabled: true, Precedence: 20, Config: []byte(`{"eodhd_api_key":"k"}`)},
		{PluginID: "massive", Category: db.PluginCategoryPrice, Enabled: false, Precedence: 10},
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	rows, err := p.ListPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].PluginID != "eodhd" || rows[0].Precedence != 20 || !rows[0].Enabled {
		t.Fatalf("first row = %+v", rows[0])
	}
	if !jsonEqual(rows[0].Config, []byte(`{"eodhd_api_key":"k"}`)) {
		t.Fatalf("config = %s", rows[0].Config)
	}
	if rows[1].PluginID != "massive" || rows[1].Precedence != 10 {
		t.Fatalf("second row = %+v", rows[1])
	}
}

// A plugin this build registers and the file does not name keeps its row and
// goes below everything the file states. An import that said nothing about a
// plugin must not leave it preferred over the ordering it did state.
func TestRestorePluginConfigs_UnnamedPluginIsDemotedNotDropped(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd", "massive", "quandl")

	// The file knows two of the three, and ranks eodhd top.
	if err := p.RestorePluginConfigs(ctx, []db.PluginConfigWithCategory{
		{PluginID: "eodhd", Category: db.PluginCategoryPrice, Enabled: true, Precedence: 2},
		{PluginID: "massive", Category: db.PluginCategoryPrice, Enabled: true, Precedence: 1},
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	rows, err := p.ListPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].PluginID != "eodhd" || rows[1].PluginID != "massive" || rows[2].PluginID != "quandl" {
		t.Fatalf("order = %s, %s, %s", rows[0].PluginID, rows[1].PluginID, rows[2].PluginID)
	}
	// Every precedence stays positive and distinct, so a later export is valid.
	seen := map[int]bool{}
	for _, r := range rows {
		if r.Precedence < 1 {
			t.Fatalf("%s has precedence %d", r.PluginID, r.Precedence)
		}
		if seen[r.Precedence] {
			t.Fatalf("duplicate precedence %d", r.Precedence)
		}
		seen[r.Precedence] = true
	}
}

// Two plugins swapping places is the case the unique constraint makes awkward,
// and the one an import does every time it restores a reordered category.
func TestRestorePluginConfigs_SwapsPrecedences(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd", "massive")

	if err := p.RestorePluginConfigs(ctx, []db.PluginConfigWithCategory{
		{PluginID: "eodhd", Category: db.PluginCategoryPrice, Precedence: 20},
		{PluginID: "massive", Category: db.PluginCategoryPrice, Precedence: 10},
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rows, err := p.ListPluginConfigs(ctx, db.PluginCategoryPrice)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if rows[0].PluginID != "eodhd" {
		t.Fatalf("expected eodhd on top, got %s", rows[0].PluginID)
	}
}

// One category's rows do not disturb another's.
func TestRestorePluginConfigs_LeavesOtherCategoriesAlone(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd")
	seedPluginConfigs(t, p, db.PluginCategoryInflation, "ons")

	if err := p.RestorePluginConfigs(ctx, []db.PluginConfigWithCategory{
		{PluginID: "eodhd", Category: db.PluginCategoryPrice, Enabled: true, Precedence: 5},
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rows, err := p.ListPluginConfigs(ctx, db.PluginCategoryInflation)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Precedence != 10 || rows[0].Enabled {
		t.Fatalf("inflation row disturbed: %+v", rows[0])
	}
}

// A row with no config row to update is the caller's mistake -- the import
// rejects unregistered plugins before it gets here -- and fails rather than
// silently doing nothing.
func TestRestorePluginConfigs_UnknownPluginFails(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	seedPluginConfigs(t, p, db.PluginCategoryPrice, "eodhd")

	err := p.RestorePluginConfigs(ctx, []db.PluginConfigWithCategory{
		{PluginID: "quandl", Category: db.PluginCategoryPrice, Precedence: 5},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}
