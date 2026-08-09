package archiveimport

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
)

func pluginConfigPart(configs ...*archivev1.PluginConfig) *archivev1.PluginConfigPart {
	return &archivev1.PluginConfigPart{Configs: configs}
}

// expectRegistered makes the registration lookup report the given rows as ones
// this build has, which is what the service creates at startup.
func expectRegistered(database *mock.MockDB, rows ...db.PluginConfigWithCategory) {
	database.EXPECT().ListAllPluginConfigs(gomock.Any()).Return(rows, nil)
}

func TestPluginConfigPart_AppliesTheFilesRows(t *testing.T) {
	database, rep := newPartTest(t)
	expectRegistered(database, db.PluginConfigWithCategory{PluginID: "eodhd", Category: "price"})
	database.EXPECT().RestorePluginConfigs(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, rows []db.PluginConfigWithCategory) error {
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			if rows[0].Category != "price" {
				t.Errorf("category = %q, want the column's spelling", rows[0].Category)
			}
			if !rows[0].Enabled || rows[0].Precedence != 20 {
				t.Errorf("row = %+v", rows[0])
			}
			if string(rows[0].Config) != `{"eodhd_api_key":"k"}` {
				t.Errorf("config = %s", rows[0].Config)
			}
			if rows[0].MaxHistoryDays == nil || *rows[0].MaxHistoryDays != 3650 {
				t.Errorf("max_history_days = %v", rows[0].MaxHistoryDays)
			}
			return nil
		})

	part := pluginConfigPart(&archivev1.PluginConfig{
		PluginId: "eodhd", Category: typev1.PluginCategory_PRICE, Enabled: true,
		Precedence: 20, ConfigJson: proto.String(`{"eodhd_api_key":"k"}`),
		MaxHistoryDays: proto.Int32(3650),
	})
	applied, err := PluginConfigPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("PluginConfigPart: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
}

// A config row for a plugin nothing will ever read is not worth storing, and
// the mismatch is worth saying out loud.
func TestPluginConfigPart_UnregisteredPluginIsRejected(t *testing.T) {
	database, rep := newPartTest(t)
	expectRegistered(database, db.PluginConfigWithCategory{PluginID: "eodhd", Category: "price"})
	database.EXPECT().RestorePluginConfigs(gomock.Any(), gomock.Len(1)).Return(nil)

	part := pluginConfigPart(
		&archivev1.PluginConfig{PluginId: "quandl", Category: typev1.PluginCategory_PRICE, Precedence: 30},
		&archivev1.PluginConfig{PluginId: "eodhd", Category: typev1.PluginCategory_PRICE, Precedence: 20},
	)
	applied, err := PluginConfigPart(context.Background(), database, part, rep)
	if err != nil {
		t.Fatalf("PluginConfigPart: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "plugin_id" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

// The same plugin id in a category it does not hold a row for is the same
// mismatch: registration is per (category, plugin), not per plugin.
func TestPluginConfigPart_RegistrationIsPerCategory(t *testing.T) {
	database, rep := newPartTest(t)
	expectRegistered(database, db.PluginConfigWithCategory{PluginID: "eodhd", Category: "price"})
	database.EXPECT().RestorePluginConfigs(gomock.Any(), gomock.Len(0)).Return(nil)

	part := pluginConfigPart(&archivev1.PluginConfig{
		PluginId: "eodhd", Category: typev1.PluginCategory_INFLATION, Precedence: 10,
	})
	if _, err := PluginConfigPart(context.Background(), database, part, rep); err != nil {
		t.Fatalf("PluginConfigPart: %v", err)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

func TestPluginConfigPart_WriteFailureFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	expectRegistered(database, db.PluginConfigWithCategory{PluginID: "eodhd", Category: "price"})
	database.EXPECT().RestorePluginConfigs(gomock.Any(), gomock.Any()).Return(errors.New("boom"))

	part := pluginConfigPart(&archivev1.PluginConfig{
		PluginId: "eodhd", Category: typev1.PluginCategory_PRICE, Precedence: 20,
	})
	if _, err := PluginConfigPart(context.Background(), database, part, rep); err == nil {
		t.Fatal("expected the part to fail")
	}
}

func TestPluginConfigPart_EmptyPart(t *testing.T) {
	database, rep := newPartTest(t)
	applied, err := PluginConfigPart(context.Background(), database, pluginConfigPart(), rep)
	if err != nil || applied != 0 {
		t.Fatalf("PluginConfigPart = %d, %v", applied, err)
	}
}
