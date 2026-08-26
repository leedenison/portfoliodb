package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/leedenison/portfoliodb/server/db"
)

// Instance settings: what an admin configures about this deployment as a whole.
//
// Text in and text out. The table holds settings with nothing in common but who
// sets them, so there is no type here worth enforcing, and the typed reader below
// is the shape every other one should take: parse it and range-check it where it
// is used, because whoever reads a number is the only one who knows what range
// makes sense for it.

// ListSettings implements db.SettingsDB.
func (p *Postgres) ListSettings(ctx context.Context) ([]db.Setting, error) {
	var rows []db.Setting
	if err := p.q.SelectContext(ctx, &rows, `SELECT key, value FROM settings ORDER BY key`); err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	return rows, nil
}

// GetSetting implements db.SettingsDB.
func (p *Postgres) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := p.q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("setting %q is not set", key)
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting implements db.SettingsDB.
func (p *Postgres) SetSetting(ctx context.Context, key, value string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// PromotionThreshold implements db.SettingsDB.
func (p *Postgres) PromotionThreshold(ctx context.Context) (int, error) {
	raw, err := p.GetSetting(ctx, db.SettingPromotionThreshold)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("promotion threshold %q is not a number", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("promotion threshold is %d, want at least 1", n)
	}
	return n, nil
}
