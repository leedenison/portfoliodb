package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// updatePluginConfig builds a single atomic UPDATE for a *_plugin_config table,
// setting only non-nil fields. Used by all three plugin config tables.
func updatePluginConfig(ctx context.Context, p *Postgres, table, pluginID string, enabled *bool, precedence *int, config []byte) (*db.PluginConfigRowFull, error) {
	if enabled == nil && precedence == nil && config == nil {
		// Nothing to update; just return current row.
		var r db.PluginConfigRowFull
		var configVal sql.NullString
		err := p.q.QueryRowContext(ctx,
			fmt.Sprintf(`SELECT plugin_id, enabled, precedence, config FROM %s WHERE plugin_id = $1`, table), pluginID).
			Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal)
		if err != nil {
			return nil, err
		}
		if configVal.Valid {
			r.Config = []byte(configVal.String)
		}
		return &r, nil
	}

	// Build a single UPDATE with only the provided fields.
	var setClauses []string
	var args []interface{}
	argN := 1

	if enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argN))
		args = append(args, *enabled)
		argN++
	}
	if precedence != nil {
		setClauses = append(setClauses, fmt.Sprintf("precedence = $%d", argN))
		args = append(args, *precedence)
		argN++
	}
	if config != nil {
		payload := config
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		setClauses = append(setClauses, fmt.Sprintf("config = $%d", argN))
		args = append(args, payload)
		argN++
	}

	args = append(args, pluginID)
	query := fmt.Sprintf(`UPDATE %s SET %s WHERE plugin_id = $%d
		RETURNING plugin_id, enabled, precedence, config`,
		table, strings.Join(setClauses, ", "), argN)

	var r db.PluginConfigRowFull
	var configVal sql.NullString
	err := p.q.QueryRowContext(ctx, query, args...).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal)
	if err != nil {
		return nil, err
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	return &r, nil
}

// UpdatePluginConfig implements db.InstrumentDB.
func (p *Postgres) UpdatePluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte) (*db.PluginConfigRowFull, error) {
	return updatePluginConfig(ctx, p, "identifier_plugin_config", pluginID, enabled, precedence, config)
}
