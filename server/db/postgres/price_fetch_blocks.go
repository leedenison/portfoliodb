package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/lib/pq"
)

// ListPriceFetchBlocks implements db.PriceFetchBlockDB.
//
// The blocked line, with the security above it: the admin surface names a block
// by the security and needs the currency to tell two of its lines apart.
func (p *Postgres) ListPriceFetchBlocks(ctx context.Context) ([]db.PriceFetchBlock, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT b.listing_id, l.instrument_id, COALESCE(l.currency, ''),
			b.plugin_id, b.reason, b.first_blocked_at
		FROM price_fetch_blocks b
		JOIN instrument_listings l ON l.id = b.listing_id
		ORDER BY b.first_blocked_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list price fetch blocks: %w", err)
	}
	defer rows.Close()
	var out []db.PriceFetchBlock
	for rows.Next() {
		var b db.PriceFetchBlock
		if err := rows.Scan(&b.ListingID, &b.InstrumentID, &b.Currency,
			&b.PluginID, &b.Reason, &b.FirstBlockedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BlockedPluginsForListings implements db.PriceFetchBlockDB.
func (p *Postgres) BlockedPluginsForListings(ctx context.Context, listingIDs []string) (map[string]map[string]bool, error) {
	if len(listingIDs) == 0 {
		return nil, nil
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT listing_id, plugin_id FROM price_fetch_blocks
		WHERE listing_id = ANY($1)
	`, pq.Array(listingIDs))
	if err != nil {
		return nil, fmt.Errorf("blocked plugins for listings: %w", err)
	}
	defer rows.Close()
	out := make(map[string]map[string]bool)
	for rows.Next() {
		var listingID, pluginID string
		if err := rows.Scan(&listingID, &pluginID); err != nil {
			return nil, err
		}
		if out[listingID] == nil {
			out[listingID] = make(map[string]bool)
		}
		out[listingID][pluginID] = true
	}
	return out, rows.Err()
}

// CreatePriceFetchBlock implements db.PriceFetchBlockDB.
func (p *Postgres) CreatePriceFetchBlock(ctx context.Context, listingID, pluginID, reason string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO price_fetch_blocks (listing_id, plugin_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (listing_id, plugin_id)
		DO UPDATE SET reason = EXCLUDED.reason
	`, listingID, pluginID, reason)
	if err != nil {
		return fmt.Errorf("create price fetch block: %w", err)
	}
	return nil
}

// DeletePriceFetchBlock implements db.PriceFetchBlockDB.
func (p *Postgres) DeletePriceFetchBlock(ctx context.Context, listingID, pluginID string) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM price_fetch_blocks WHERE listing_id = $1 AND plugin_id = $2
	`, listingID, pluginID)
	if err != nil {
		return fmt.Errorf("delete price fetch block: %w", err)
	}
	return nil
}

// ListPriceFetchBlocksForExport implements db.PriceFetchBlockDB.
func (p *Postgres) ListPriceFetchBlocksForExport(ctx context.Context) ([]db.ExportFetchBlock, error) {
	return listFetchBlocksForExport(ctx, p, priceFetchBlocks)
}

// UpsertPriceFetchBlocks implements db.PriceFetchBlockDB.
func (p *Postgres) UpsertPriceFetchBlocks(ctx context.Context, blocks []db.FetchBlockInput) error {
	return upsertFetchBlocks(ctx, p, priceFetchBlocks, blocks)
}

// fetchBlockTable names one of the two fetch-block tables and the column its
// blocks hang off, in the pattern coverageTable follows. The owner differs
// because the fetchers do: a price is fetched for one currency line, and a
// corporate event is an action on the security and is fetched once for all of
// its lines.
type fetchBlockTable struct {
	name  string
	owner string
	// listingOwned says whether owner is a listing, which is what decides
	// whether an exported block carries a currency and how the join reaches the
	// security.
	listingOwned bool
}

var (
	priceFetchBlocks          = fetchBlockTable{name: "price_fetch_blocks", owner: "listing_id", listingOwned: true}
	corporateEventFetchBlocks = fetchBlockTable{name: "corporate_event_fetch_blocks", owner: "instrument_id"}
)

// listFetchBlocksForExport reads one fetch-block table with the best identifier
// of the security each block belongs to. The two tables have the same columns
// apart from what they hang off, so they share the query: a block that meant
// different things in each would be a reason to split them, and it does not.
//
// A price block also carries the currency of the line it blocks, which is what a
// file needs to name that line. A corporate event block leaves it empty.
func listFetchBlocksForExport(ctx context.Context, p *Postgres, t fetchBlockTable) ([]db.ExportFetchBlock, error) {
	currency, join := `'' AS currency`, `JOIN instruments i ON i.id = b.instrument_id`
	if t.listingOwned {
		currency = `COALESCE(l.currency, '') AS currency`
		join = `JOIN instrument_listings l ON l.id = b.listing_id
		JOIN instruments i ON i.id = l.instrument_id`
	}
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			` + currency + `,
			b.plugin_id, b.reason, b.first_blocked_at
		FROM ` + t.name + ` b
		` + join + `
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), currency, b.plugin_id
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list %s for export: %w", t.name, err)
	}
	defer rows.Close()

	var out []db.ExportFetchBlock
	for rows.Next() {
		var r db.ExportFetchBlock
		if err := rows.Scan(&r.Ref.Type, &r.Ref.Value, &r.Ref.Domain, &r.Currency,
			&r.PluginID, &r.Reason, &r.FirstBlockedAt); err != nil {
			return nil, fmt.Errorf("scan %s for export: %w", t.name, err)
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
func upsertFetchBlocks(ctx context.Context, p *Postgres, t fetchBlockTable, blocks []db.FetchBlockInput) error {
	if len(blocks) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, `INSERT INTO %s (%s, plugin_id, reason, first_blocked_at) VALUES `, t.name, t.owner)
	args := make([]interface{}, 0, len(blocks)*4)
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 4
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4)
		args = append(args, blk.OwnerID, blk.PluginID, blk.Reason, blk.FirstBlockedAt)
	}
	fmt.Fprintf(&b, ` ON CONFLICT (%s, plugin_id) DO UPDATE SET
		reason = EXCLUDED.reason,
		first_blocked_at = LEAST(%s.first_blocked_at, EXCLUDED.first_blocked_at)`, t.owner, t.name)

	if _, err := p.q.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert %s: %w", t.name, err)
	}
	return nil
}
