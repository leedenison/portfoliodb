package archiveimport

import (
	"context"
	"fmt"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// PluginConfigPart restores an archive's plugin configuration and reports
// through rep. It returns how many rows were applied.
//
// A row naming a plugin this build does not register is rejected rather than
// stored: nothing would ever read it, and the mismatch is worth saying out loud.
// Registration is read off the config table rather than off the five plugin
// registries -- the service creates a row for every plugin it registers at
// startup, so a missing row is exactly a plugin this build does not have, and
// the part needs no registry threaded through it to know that.
//
// This is the part that carries live API keys. Nothing here redacts them: a
// rebuild that needs an admin to re-enter every provider credential by hand is
// a rebuild that has not restored.
func PluginConfigPart(ctx context.Context, database db.DB, part *archivev1.PluginConfigPart, rep *PartReporter) (int32, error) {
	configs := part.GetConfigs()
	rep.Total(ctx, len(configs))
	if len(configs) == 0 {
		return 0, nil
	}

	existing, err := database.ListAllPluginConfigs(ctx)
	if err != nil {
		return 0, fmt.Errorf("read plugin configs: %w", err)
	}
	known := make(map[string]bool, len(existing))
	for _, e := range existing {
		known[e.Category+"\x00"+e.PluginID] = true
	}

	rows := make([]db.PluginConfigWithCategory, 0, len(configs))
	for i, c := range configs {
		category := db.PluginCategoryToStr(c.GetCategory())
		if category == "" {
			rep.Errf(i, "category", fmt.Sprintf("%s: unknown plugin category", c.GetPluginId()))
			rep.Advance(ctx, 1)
			continue
		}
		if !known[category+"\x00"+c.GetPluginId()] {
			rep.Errf(i, "plugin_id", fmt.Sprintf("%s is not a %s plugin this build registers", c.GetPluginId(), category))
			rep.Advance(ctx, 1)
			continue
		}
		row := db.PluginConfigWithCategory{
			PluginID:   c.GetPluginId(),
			Category:   category,
			Enabled:    c.GetEnabled(),
			Precedence: int(c.GetPrecedence()),
		}
		if c.ConfigJson != nil {
			row.Config = []byte(c.GetConfigJson())
		}
		if c.MaxHistoryDays != nil {
			v := int(c.GetMaxHistoryDays())
			row.MaxHistoryDays = &v
		}
		rows = append(rows, row)
		rep.Advance(ctx, 1)
	}

	if err := database.RestorePluginConfigs(ctx, rows); err != nil {
		return 0, fmt.Errorf("restore plugin configs: %w", err)
	}
	return int32(len(rows)), nil
}
