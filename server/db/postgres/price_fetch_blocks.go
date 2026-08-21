package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/lib/pq"
)

// ListPriceFetchBlocks implements db.PriceFetchBlockDB.
func (p *Postgres) ListPriceFetchBlocks(ctx context.Context) ([]db.PriceFetchBlock, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id, reason, first_blocked_at
		FROM price_fetch_blocks ORDER BY first_blocked_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list price fetch blocks: %w", err)
	}
	defer rows.Close()
	var out []db.PriceFetchBlock
	for rows.Next() {
		var b db.PriceFetchBlock
		if err := rows.Scan(&b.InstrumentID, &b.PluginID, &b.Reason, &b.FirstBlockedAt); err != nil {
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
		DO UPDATE SET reason = EXCLUDED.reason
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

// ListPriceFetchBlocksForExport implements db.PriceFetchBlockDB.
func (p *Postgres) ListPriceFetchBlocksForExport(ctx context.Context) ([]db.ExportFetchBlock, error) {
	return listFetchBlocksForExport(ctx, p, "price_fetch_blocks")
}

// UpsertPriceFetchBlocks implements db.PriceFetchBlockDB.
func (p *Postgres) UpsertPriceFetchBlocks(ctx context.Context, blocks []db.FetchBlockInput) error {
	return upsertFetchBlocks(ctx, p, "price_fetch_blocks", blocks)
}

// listFetchBlocksForExport reads one fetch-block table with the best identifier
// per instrument. The two tables have the same columns, so they share the
// query: a block that meant different things in each would be a reason to split
// them, and it does not.
func listFetchBlocksForExport(ctx context.Context, p *Postgres, table string) ([]db.ExportFetchBlock, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			b.plugin_id, b.reason, b.first_blocked_at
		FROM ` + table + ` b
		JOIN instruments i ON i.id = b.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), b.plugin_id
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list %s for export: %w", table, err)
	}
	defer rows.Close()

	var out []db.ExportFetchBlock
	for rows.Next() {
		var r db.ExportFetchBlock
		if err := rows.Scan(&r.Ref.Type, &r.Ref.Value, &r.Ref.Domain,
			&r.PluginID, &r.Reason, &r.FirstBlockedAt); err != nil {
			return nil, fmt.Errorf("scan %s for export: %w", table, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// upsertFetchBlocks writes blocks into one fetch-block table.
//
// first_blocked_at takes the earlier of the stored and the supplied value. The
// column records when the pair was first blocked and is never overwritten, so
// an import can move it backwards and must not move it forwards.
func upsertFetchBlocks(ctx context.Context, p *Postgres, table string, blocks []db.FetchBlockInput) error {
	if len(blocks) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, `INSERT INTO %s (instrument_id, plugin_id, reason, first_blocked_at) VALUES `, table)
	args := make([]interface{}, 0, len(blocks)*4)
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 4
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4)
		args = append(args, blk.InstrumentID, blk.PluginID, blk.Reason, blk.FirstBlockedAt)
	}
	fmt.Fprintf(&b, ` ON CONFLICT (instrument_id, plugin_id) DO UPDATE SET
		reason = EXCLUDED.reason,
		first_blocked_at = LEAST(%s.first_blocked_at, EXCLUDED.first_blocked_at)`, table)

	if _, err := p.q.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert %s: %w", table, err)
	}
	return nil
}
