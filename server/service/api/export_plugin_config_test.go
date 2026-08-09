package api

import (
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
)

func exportPluginConfigs(t *testing.T, rows []dbpkg.PluginConfigWithCategory) []*archivev1.PluginConfig {
	t.Helper()
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListAllPluginConfigs(gomock.Any()).Return(rows, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PLUGIN_CONFIG},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	return stream.pluginConfigs()
}

// The column spells a category lowercase and the file spells it by its enum
// name. This is the one shared vocabulary where the two differ.
func TestExportSystemArchive_PluginConfig_MapsTheCategorySpelling(t *testing.T) {
	maxHist := 3650
	configs := exportPluginConfigs(t, []dbpkg.PluginConfigWithCategory{{
		PluginID: "eodhd", Category: "corporate_event", Enabled: true,
		Precedence: 20, Config: []byte(`{"eodhd_api_key":"secret"}`), MaxHistoryDays: &maxHist,
	}})
	if len(configs) != 1 {
		t.Fatalf("expected 1 row, got %d", len(configs))
	}
	if configs[0].GetCategory() != typev1.PluginCategory_CORPORATE_EVENT {
		t.Fatalf("category = %s", configs[0].GetCategory())
	}
	if configs[0].GetPrecedence() != 20 || !configs[0].GetEnabled() {
		t.Fatalf("row = %+v", configs[0])
	}
	if configs[0].GetMaxHistoryDays() != 3650 {
		t.Fatalf("max_history_days = %d", configs[0].GetMaxHistoryDays())
	}
}

// The whole point of the part: a rebuild needs no manual key re-entry, so the
// key travels unredacted. This is also what makes the file a secret.
func TestExportSystemArchive_PluginConfig_CarriesTheKeyUnredacted(t *testing.T) {
	configs := exportPluginConfigs(t, []dbpkg.PluginConfigWithCategory{{
		PluginID: "eodhd", Category: "price", Enabled: true, Precedence: 20,
		Config: []byte(`{"eodhd_api_key":"live-key-abc123"}`),
	}})
	if !strings.Contains(configs[0].GetConfigJson(), "live-key-abc123") {
		t.Fatalf("config_json = %q", configs[0].GetConfigJson())
	}
}

// Absent is not zero: unlimited lookback is a different statement from a
// lookback of no days.
func TestExportSystemArchive_PluginConfig_OmitsAbsentMaxHistory(t *testing.T) {
	configs := exportPluginConfigs(t, []dbpkg.PluginConfigWithCategory{{
		PluginID: "cash", Category: "identifier", Precedence: 10,
	}})
	if configs[0].MaxHistoryDays != nil {
		t.Fatalf("max_history_days = %v, want absent", configs[0].MaxHistoryDays)
	}
	if configs[0].ConfigJson != nil {
		t.Fatalf("config_json = %v, want absent", configs[0].ConfigJson)
	}
}

// The part is off by default in the menu, not absent from the export: an admin
// who asks for it gets it.
func TestExportSystemArchive_PluginConfig_EmptyIsAnEmptyPart(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListAllPluginConfigs(gomock.Any()).Return(nil, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PLUGIN_CONFIG},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	want := []string{"envelope", "begin:PLUGIN_CONFIG"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}
