package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
)

// ListEnabledPluginConfigs implements db.PluginConfigDB.
func (p *Postgres) ListEnabledPluginConfigs(ctx context.Context, category string) ([]db.PluginConfigRow, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, precedence, config, max_history_days FROM plugin_config
		WHERE category = $1 AND enabled = true ORDER BY precedence DESC
	`, category)
	if err != nil {
		return nil, fmt.Errorf("list enabled plugin configs (%s): %w", category, err)
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

// ListPluginConfigs implements db.PluginConfigDB.
func (p *Postgres) ListPluginConfigs(ctx context.Context, category string) ([]db.PluginConfigRowFull, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, enabled, precedence, config, max_history_days FROM plugin_config
		WHERE category = $1 ORDER BY precedence DESC
	`, category)
	if err != nil {
		return nil, fmt.Errorf("list plugin configs (%s): %w", category, err)
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

// GetPluginConfig implements db.PluginConfigDB.
func (p *Postgres) GetPluginConfig(ctx context.Context, category, pluginID string) (*db.PluginConfigRowFull, error) {
	var r db.PluginConfigRowFull
	var configVal sql.NullString
	var maxHist sql.NullInt32
	err := p.q.QueryRowContext(ctx,
		`SELECT plugin_id, enabled, precedence, config, max_history_days FROM plugin_config WHERE category = $1 AND plugin_id = $2`,
		category, pluginID).
		Scan(&r.PluginID, &r.Enabled, &r.Precedence, &configVal, &maxHist)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("get plugin config (%s, %s): %w", category, pluginID, err)
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

// InsertPluginConfig implements db.PluginConfigDB.
func (p *Postgres) InsertPluginConfig(ctx context.Context, category, pluginID string, enabled bool, precedence int, config []byte, maxHistoryDays *int) (*db.PluginConfigRowFull, error) {
	payload := config
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	var maxHist sql.NullInt32
	if maxHistoryDays != nil {
		maxHist = sql.NullInt32{Int32: int32(*maxHistoryDays), Valid: true}
	}
	_, err := p.q.ExecContext(ctx,
		`INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config, max_history_days) VALUES ($1, $2, $3, $4, $5, $6)`,
		pluginID, category, enabled, precedence, payload, maxHist)
	if err != nil {
		return nil, fmt.Errorf("insert plugin config (%s, %s): %w", category, pluginID, err)
	}
	return p.GetPluginConfig(ctx, category, pluginID)
}

// UpdatePluginConfig implements db.PluginConfigDB.
func (p *Postgres) UpdatePluginConfig(ctx context.Context, category, pluginID string, enabled *bool, precedence *int, config []byte, maxHistoryDays *int) (*db.PluginConfigRowFull, error) {
	if enabled == nil && precedence == nil && config == nil && maxHistoryDays == nil {
		return p.GetPluginConfig(ctx, category, pluginID)
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

	args = append(args, category, pluginID)
	query := fmt.Sprintf(`UPDATE plugin_config SET %s WHERE category = $%d AND plugin_id = $%d
		RETURNING plugin_id, enabled, precedence, config, max_history_days`,
		strings.Join(setClauses, ", "), argN, argN+1)

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

// ReorderPluginConfigs implements db.PluginConfigDB.
func (p *Postgres) ReorderPluginConfigs(ctx context.Context, category string, pluginIDs []string) error {
	return p.runInTx(ctx, func(exec queryable) error {
		if _, err := exec.ExecContext(ctx, `SET CONSTRAINTS plugin_config_category_precedence_key DEFERRED`); err != nil {
			return fmt.Errorf("defer constraints: %w", err)
		}
		for i, pid := range pluginIDs {
			prec := len(pluginIDs) - i
			res, err := exec.ExecContext(ctx,
				`UPDATE plugin_config SET precedence = $1 WHERE category = $2 AND plugin_id = $3`,
				prec, category, pid)
			if err != nil {
				return fmt.Errorf("update precedence for %s: %w", pid, err)
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				return fmt.Errorf("plugin %s not found in category %s", pid, category)
			}
		}
		return nil
	})
}
