package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/leedenison/portfoliodb/server/db"
)

// ListEnabledPluginConfigs implements db.InstrumentDB.
func (p *Postgres) ListEnabledPluginConfigs(ctx context.Context) ([]db.PluginConfigRow, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, precedence, config FROM identifier_plugin_config
		WHERE enabled = true ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled plugin configs: %w", err)
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

// ListPluginConfigs implements db.InstrumentDB.
func (p *Postgres) ListPluginConfigs(ctx context.Context) ([]db.PluginConfigRowFull, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, enabled, precedence, config FROM identifier_plugin_config
		ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRowFull
	for rows.Next() {
		var r db.PluginConfigRowFull
		var config sql.NullString
		if err := rows.Scan(&r.PluginID, &r.Enabled, &r.Precedence, &config); err != nil {
			return nil, err
		}
		if config.Valid {
			r.Config = []byte(config.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPluginConfig implements db.InstrumentDB.
func (p *Postgres) GetPluginConfig(ctx context.Context, pluginID string) (*db.PluginConfigRowFull, error) {
	var r db.PluginConfigRowFull
	var configVal sql.NullString
	err := p.q.QueryRowContext(ctx, `SELECT plugin_id, enabled, precedence, config FROM identifier_plugin_config WHERE plugin_id = $1`, pluginID).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get plugin config: %w", err)
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	return &r, nil
}

// InsertPluginConfig implements db.InstrumentDB.
func (p *Postgres) InsertPluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte) (*db.PluginConfigRowFull, error) {
	payload := config
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := p.q.ExecContext(ctx, `INSERT INTO identifier_plugin_config (plugin_id, enabled, precedence, config) VALUES ($1, $2, $3, $4)`,
		pluginID, enabled, precedence, payload)
	if err != nil {
		return nil, fmt.Errorf("insert plugin config: %w", err)
	}
	return p.GetPluginConfig(ctx, pluginID)
}

// UpdatePluginConfig implements db.InstrumentDB.
func (p *Postgres) UpdatePluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte) (*db.PluginConfigRowFull, error) {
	if enabled != nil {
		if _, err := p.q.ExecContext(ctx, `UPDATE identifier_plugin_config SET enabled = $1 WHERE plugin_id = $2`, *enabled, pluginID); err != nil {
			return nil, fmt.Errorf("update plugin enabled: %w", err)
		}
	}
	if precedence != nil {
		if _, err := p.q.ExecContext(ctx, `UPDATE identifier_plugin_config SET precedence = $1 WHERE plugin_id = $2`, *precedence, pluginID); err != nil {
			return nil, fmt.Errorf("update plugin precedence: %w", err)
		}
	}
	if config != nil {
		payload := config
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		if _, err := p.q.ExecContext(ctx, `UPDATE identifier_plugin_config SET config = $1 WHERE plugin_id = $2`, payload, pluginID); err != nil {
			return nil, fmt.Errorf("update plugin config: %w", err)
		}
	}
	// Return updated row
	rows, err := p.q.QueryContext(ctx, `SELECT plugin_id, enabled, precedence, config FROM identifier_plugin_config WHERE plugin_id = $1`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("plugin_id %q not found", pluginID)
	}
	var r db.PluginConfigRowFull
	var configVal sql.NullString
	if err := rows.Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal); err != nil {
		return nil, err
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	return &r, rows.Err()
}
