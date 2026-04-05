package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/leedenison/portfoliodb/server/db"
)

// DistinctDisplayCurrencies implements db.InflationIndexDB.
func (p *Postgres) DistinctDisplayCurrencies(ctx context.Context) ([]string, error) {
	const q = `SELECT DISTINCT display_currency FROM users WHERE display_currency IS NOT NULL AND display_currency != '' ORDER BY display_currency`
	var currencies []string
	if err := p.q.SelectContext(ctx, &currencies, q); err != nil {
		return nil, fmt.Errorf("distinct display currencies: %w", err)
	}
	return currencies, nil
}

// InflationCoverage implements db.InflationIndexDB.
func (p *Postgres) InflationCoverage(ctx context.Context, currency string) ([]time.Time, error) {
	const q = `SELECT month FROM inflation_indices WHERE currency = $1 ORDER BY month`
	var months []time.Time
	if err := p.q.SelectContext(ctx, &months, q, currency); err != nil {
		return nil, fmt.Errorf("inflation coverage: %w", err)
	}
	return months, nil
}

// UpsertInflationIndices implements db.InflationIndexDB.
func (p *Postgres) UpsertInflationIndices(ctx context.Context, indices []db.InflationIndex) error {
	if len(indices) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO inflation_indices (currency, month, index_value, base_year, data_provider, fetched_at)
		VALUES `)
	args := make([]interface{}, 0, len(indices)*5)
	for i, idx := range indices {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 5
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, now())", base+1, base+2, base+3, base+4, base+5)
		args = append(args, idx.Currency, idx.Month, idx.IndexValue, idx.BaseYear, idx.DataProvider)
	}
	b.WriteString(` ON CONFLICT (currency, month) DO UPDATE SET
		index_value = EXCLUDED.index_value,
		base_year = EXCLUDED.base_year,
		data_provider = EXCLUDED.data_provider,
		fetched_at = now()`)

	if _, err := p.q.ExecContext(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("upsert inflation indices: %w", err)
	}
	return nil
}

// ListInflationIndices implements db.InflationIndexDB.
func (p *Postgres) ListInflationIndices(ctx context.Context, currency string, dateFrom, dateTo *time.Time, pageSize int, pageToken string) ([]db.InflationIndex, string, int, error) {
	offset := decodePageToken(pageToken)

	where := sq.And{}
	if currency != "" {
		where = append(where, sq.Eq{"currency": currency})
	}
	if dateFrom != nil {
		where = append(where, sq.GtOrEq{"month": *dateFrom})
	}
	if dateTo != nil {
		where = append(where, sq.LtOrEq{"month": *dateTo})
	}

	countQ, countArgs, err := psql.Select("COUNT(*)").From("inflation_indices").Where(where).ToSql()
	if err != nil {
		return nil, "", 0, fmt.Errorf("build count inflation query: %w", err)
	}
	var total int
	if err := p.q.QueryRowContext(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, "", 0, fmt.Errorf("count inflation indices: %w", err)
	}
	if total == 0 {
		return nil, "", 0, nil
	}

	q, args, err := psql.Select("currency", "month", "index_value", "base_year", "data_provider").
		From("inflation_indices").
		Where(where).
		OrderBy("month DESC", "currency").
		Limit(uint64(pageSize + 1)).Offset(uint64(offset)).
		ToSql()
	if err != nil {
		return nil, "", 0, fmt.Errorf("build list inflation query: %w", err)
	}

	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list inflation indices: %w", err)
	}
	defer rows.Close()

	var results []db.InflationIndex
	for rows.Next() {
		var r db.InflationIndex
		if err := rows.Scan(&r.Currency, &r.Month, &r.IndexValue, &r.BaseYear, &r.DataProvider); err != nil {
			return nil, "", 0, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}

	var nextToken string
	if len(results) > pageSize {
		results = results[:pageSize]
		nextToken = encodePageToken(offset + int64(pageSize))
	}

	return results, nextToken, total, nil
}
