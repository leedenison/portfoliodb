package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/leedenison/portfoliodb/server/db"
)

// ListEnabledDescriptionPluginConfigs implements db.DescriptionPluginDB.
func (p *Postgres) ListEnabledDescriptionPluginConfigs(ctx context.Context) ([]db.PluginConfigRow, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, precedence, config FROM description_plugin_config
		WHERE enabled = true ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled description plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRow
	for rows.Next() {
		var r db.PluginConfigRow
		var config sql.NullString
		if err := rows.Scan(&r.PluginID, &r.Precedence, &config); err != nil {
			return nil, err
		}
		if config.Valid {
			r.Config = []byte(config.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListDescriptionPluginConfigs implements db.DescriptionPluginDB.
func (p *Postgres) ListDescriptionPluginConfigs(ctx context.Context) ([]db.PluginConfigRowFull, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, enabled, precedence, config FROM description_plugin_config
		ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list description plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRowFull
	for rows.Next() {
		var r db.PluginConfigRowFull
		var configVal sql.NullString
		if err := rows.Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal); err != nil {
			return nil, err
		}
		if configVal.Valid {
			r.Config = []byte(configVal.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetDescriptionPluginConfig implements db.DescriptionPluginDB.
func (p *Postgres) GetDescriptionPluginConfig(ctx context.Context, pluginID string) (*db.PluginConfigRowFull, error) {
	var r db.PluginConfigRowFull
	var configVal sql.NullString
	err := p.q.QueryRowContext(ctx, `SELECT plugin_id, enabled, precedence, config FROM description_plugin_config WHERE plugin_id = $1`, pluginID).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get description plugin config: %w", err)
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	return &r, nil
}

// InsertDescriptionPluginConfig implements db.DescriptionPluginDB.
func (p *Postgres) InsertDescriptionPluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte) (*db.PluginConfigRowFull, error) {
	payload := config
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := p.q.ExecContext(ctx, `INSERT INTO description_plugin_config (plugin_id, enabled, precedence, config) VALUES ($1, $2, $3, $4)`,
		pluginID, enabled, precedence, payload)
	if err != nil {
		return nil, fmt.Errorf("insert description plugin config: %w", err)
	}
	return p.GetDescriptionPluginConfig(ctx, pluginID)
}

// UpdateDescriptionPluginConfig implements db.DescriptionPluginDB.
func (p *Postgres) UpdateDescriptionPluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte) (*db.PluginConfigRowFull, error) {
	return updatePluginConfig(ctx, p, "description_plugin_config", pluginID, enabled, precedence, config)
}
