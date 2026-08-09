package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
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

	qb := psql.Update("plugin_config").
		Where(sq.Eq{"category": category, "plugin_id": pluginID}).
		Suffix("RETURNING plugin_id, enabled, precedence, config, max_history_days")

	if enabled != nil {
		qb = qb.Set("enabled", *enabled)
	}
	if precedence != nil {
		qb = qb.Set("precedence", *precedence)
	}
	if config != nil {
		payload := config
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		qb = qb.Set("config", payload)
	}
	if maxHistoryDays != nil {
		if *maxHistoryDays == 0 {
			qb = qb.Set("max_history_days", sql.NullInt32{})
		} else {
			qb = qb.Set("max_history_days", sql.NullInt32{Int32: int32(*maxHistoryDays), Valid: true})
		}
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build update plugin config query: %w", err)
	}

	var r db.PluginConfigRowFull
	var configVal sql.NullString
	var maxHist sql.NullInt32
	err = p.q.QueryRowContext(ctx, query, args...).
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

// ListAllPluginConfigs implements db.PluginConfigDB.
func (p *Postgres) ListAllPluginConfigs(ctx context.Context) ([]db.PluginConfigWithCategory, error) {
	const q = `SELECT plugin_id, category, enabled, precedence, config, max_history_days
		FROM plugin_config ORDER BY category, precedence DESC`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all plugin configs: %w", err)
	}
	defer rows.Close()

	var out []db.PluginConfigWithCategory
	for rows.Next() {
		var r db.PluginConfigWithCategory
		var config sql.NullString
		var maxHist sql.NullInt32
		if err := rows.Scan(&r.PluginID, &r.Category, &r.Enabled, &r.Precedence, &config, &maxHist); err != nil {
			return nil, fmt.Errorf("scan plugin config: %w", err)
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

// RestorePluginConfigs implements db.PluginConfigDB.
//
// Precedence is unique per category, and the rows being written already hold
// values in that space, so applying the file's numbers directly would collide
// with the instance's own. Each category is therefore rewritten in three steps
// inside one transaction:
//
//  1. Negate every precedence in the category. Negation is injective, so the
//     negatives cannot collide with each other, and they free the whole
//     positive range. This is also why the deferrable constraint does not need
//     deferring here: no step ever writes a value another row still holds.
//  2. Give each plugin the file does not name the next value from 1..k,
//     keeping their order relative to each other. They end up below everything
//     the file states, so a plugin an import said nothing about cannot come out
//     preferred over one it did.
//  3. Apply each file row with its stated precedence plus k. In the ordinary
//     case k is zero and the stored numbers are exactly the file's.
func (p *Postgres) RestorePluginConfigs(ctx context.Context, configs []db.PluginConfigWithCategory) error {
	if len(configs) == 0 {
		return nil
	}
	byCategory := make(map[string][]db.PluginConfigWithCategory)
	for _, c := range configs {
		byCategory[c.Category] = append(byCategory[c.Category], c)
	}

	return p.runInTx(ctx, func(exec queryable) error {
		for category, rows := range byCategory {
			named := make(map[string]bool, len(rows))
			for _, r := range rows {
				named[r.PluginID] = true
			}

			if _, err := exec.ExecContext(ctx,
				`UPDATE plugin_config SET precedence = -precedence WHERE category = $1`, category); err != nil {
				return fmt.Errorf("clear precedences for %s: %w", category, err)
			}

			// The unnamed rows in the order they were in, which is descending
			// precedence and therefore ascending once negated.
			unnamed, err := unnamedPluginIDs(ctx, exec, category, named)
			if err != nil {
				return err
			}
			for i, pluginID := range unnamed {
				if _, err := exec.ExecContext(ctx,
					`UPDATE plugin_config SET precedence = $1 WHERE category = $2 AND plugin_id = $3`,
					len(unnamed)-i, category, pluginID); err != nil {
					return fmt.Errorf("demote %s/%s: %w", category, pluginID, err)
				}
			}

			offset := len(unnamed)
			for _, r := range rows {
				var maxHist sql.NullInt32
				if r.MaxHistoryDays != nil {
					maxHist = sql.NullInt32{Int32: int32(*r.MaxHistoryDays), Valid: true}
				}
				config := r.Config
				if len(config) == 0 {
					config = []byte("{}")
				}
				res, err := exec.ExecContext(ctx, `
					UPDATE plugin_config
					SET enabled = $1, precedence = $2, config = $3, max_history_days = $4
					WHERE category = $5 AND plugin_id = $6`,
					r.Enabled, r.Precedence+offset, config, maxHist, category, r.PluginID)
				if err != nil {
					return fmt.Errorf("restore %s/%s: %w", category, r.PluginID, err)
				}
				n, _ := res.RowsAffected()
				if n == 0 {
					return fmt.Errorf("restore %s/%s: no such plugin config row", category, r.PluginID)
				}
			}
		}
		return nil
	})
}

// unnamedPluginIDs lists the plugins holding a row in a category that the
// archive did not name, ordered highest precedence first.
func unnamedPluginIDs(ctx context.Context, exec queryable, category string, named map[string]bool) ([]string, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT plugin_id FROM plugin_config WHERE category = $1 ORDER BY precedence`, category)
	if err != nil {
		return nil, fmt.Errorf("read plugin configs for %s: %w", category, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan plugin id for %s: %w", category, err)
		}
		if !named[id] {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
