package postgres

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"github.com/leedenison/portfoliodb/server/db"
)

// ListPriceFetchBlocks implements db.PriceFetchBlockDB.
func (p *Postgres) ListPriceFetchBlocks(ctx context.Context) ([]db.PriceFetchBlock, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id, reason, created_at
		FROM price_fetch_blocks ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list price fetch blocks: %w", err)
	}
	defer rows.Close()
	var out []db.PriceFetchBlock
	for rows.Next() {
		var b db.PriceFetchBlock
		if err := rows.Scan(&b.InstrumentID, &b.PluginID, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BlockedPluginsForInstruments implements db.PriceFetchBlockDB.
func (p *Postgres) BlockedPluginsForInstruments(ctx context.Context, instrumentIDs []string) (map[string]map[string]bool, error) {
	if len(instrumentIDs) == 0 {
		return nil, nil
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id FROM price_fetch_blocks
		WHERE instrument_id = ANY($1)
	`, pq.Array(instrumentIDs))
	if err != nil {
		return nil, fmt.Errorf("blocked plugins for instruments: %w", err)
	}
	defer rows.Close()
	out := make(map[string]map[string]bool)
	for rows.Next() {
		var instID, pluginID string
		if err := rows.Scan(&instID, &pluginID); err != nil {
			return nil, err
		}
		if out[instID] == nil {
			out[instID] = make(map[string]bool)
		}
		out[instID][pluginID] = true
	}
	return out, rows.Err()
}

// CreatePriceFetchBlock implements db.PriceFetchBlockDB.
func (p *Postgres) CreatePriceFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO price_fetch_blocks (instrument_id, plugin_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (instrument_id, plugin_id)
		DO UPDATE SET reason = EXCLUDED.reason, created_at = now()
	`, instrumentID, pluginID, reason)
	if err != nil {
		return fmt.Errorf("create price fetch block: %w", err)
	}
	return nil
}

// DeletePriceFetchBlock implements db.PriceFetchBlockDB.
func (p *Postgres) DeletePriceFetchBlock(ctx context.Context, instrumentID, pluginID string) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM price_fetch_blocks WHERE instrument_id = $1 AND plugin_id = $2
	`, instrumentID, pluginID)
	if err != nil {
		return fmt.Errorf("delete price fetch block: %w", err)
	}
	return nil
}
