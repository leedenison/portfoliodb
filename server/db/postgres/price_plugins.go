package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
)

// ListEnabledPricePluginConfigs implements db.PricePluginDB.
func (p *Postgres) ListEnabledPricePluginConfigs(ctx context.Context) ([]db.PluginConfigRow, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, precedence, config, max_history_days FROM price_plugin_config
		WHERE enabled = true ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled price plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRow
	for rows.Next() {
		var r db.PluginConfigRow
		var config sql.NullString
		var maxHist sql.NullInt32
		if err := rows.Scan(&r.PluginID, &r.Precedence, &config, &maxHist); err != nil {
			return nil, err
		}
		if config.Valid {
			r.Config = []byte(config.String)
		}
		if maxHist.Valid {
			v := int(maxHist.Int32)
			r.MaxHistoryDays = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPricePluginConfigs implements db.PricePluginDB.
func (p *Postgres) ListPricePluginConfigs(ctx context.Context) ([]db.PluginConfigRowFull, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, enabled, precedence, config, max_history_days FROM price_plugin_config
		ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list price plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRowFull
	for rows.Next() {
		var r db.PluginConfigRowFull
		var configVal sql.NullString
		var maxHist sql.NullInt32
		if err := rows.Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal, &maxHist); err != nil {
			return nil, err
		}
		if configVal.Valid {
			r.Config = []byte(configVal.String)
		}
		if maxHist.Valid {
			v := int(maxHist.Int32)
			r.MaxHistoryDays = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPricePluginConfig implements db.PricePluginDB.
func (p *Postgres) GetPricePluginConfig(ctx context.Context, pluginID string) (*db.PluginConfigRowFull, error) {
	var r db.PluginConfigRowFull
	var configVal sql.NullString
	var maxHist sql.NullInt32
	err := p.q.QueryRowContext(ctx, `SELECT plugin_id, enabled, precedence, config, max_history_days FROM price_plugin_config WHERE plugin_id = $1`, pluginID).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal, &maxHist)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get price plugin config: %w", err)
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	if maxHist.Valid {
		v := int(maxHist.Int32)
		r.MaxHistoryDays = &v
	}
	return &r, nil
}

// InsertPricePluginConfig implements db.PricePluginDB.
func (p *Postgres) InsertPricePluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte, maxHistoryDays *int) (*db.PluginConfigRowFull, error) {
	payload := config
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	var maxHist sql.NullInt32
	if maxHistoryDays != nil {
		maxHist = sql.NullInt32{Int32: int32(*maxHistoryDays), Valid: true}
	}
	_, err := p.q.ExecContext(ctx, `INSERT INTO price_plugin_config (plugin_id, enabled, precedence, config, max_history_days) VALUES ($1, $2, $3, $4, $5)`,
		pluginID, enabled, precedence, payload, maxHist)
	if err != nil {
		return nil, fmt.Errorf("insert price plugin config: %w", err)
	}
	return p.GetPricePluginConfig(ctx, pluginID)
}

// UpdatePricePluginConfig implements db.PricePluginDB. Inlines the UPDATE
// (rather than delegating to the shared helper) because of the extra
// max_history_days column.
func (p *Postgres) UpdatePricePluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte, maxHistoryDays *int) (*db.PluginConfigRowFull, error) {
	if enabled == nil && precedence == nil && config == nil && maxHistoryDays == nil {
		return p.GetPricePluginConfig(ctx, pluginID)
	}

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
	if maxHistoryDays != nil {
		if *maxHistoryDays == 0 {
			setClauses = append(setClauses, fmt.Sprintf("max_history_days = $%d", argN))
			args = append(args, sql.NullInt32{})
		} else {
			setClauses = append(setClauses, fmt.Sprintf("max_history_days = $%d", argN))
			args = append(args, sql.NullInt32{Int32: int32(*maxHistoryDays), Valid: true})
		}
		argN++
	}

	args = append(args, pluginID)
	query := fmt.Sprintf(`UPDATE price_plugin_config SET %s WHERE plugin_id = $%d
		RETURNING plugin_id, enabled, precedence, config, max_history_days`,
		strings.Join(setClauses, ", "), argN)

	var r db.PluginConfigRowFull
	var configVal sql.NullString
	var maxHist sql.NullInt32
	err := p.q.QueryRowContext(ctx, query, args...).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal, &maxHist)
	if err != nil {
		return nil, err
	}
	if configVal.Valid {
		r.Config = []byte(configVal.String)
	}
	if maxHist.Valid {
		v := int(maxHist.Int32)
		r.MaxHistoryDays = &v
	}
	return &r, nil
}
