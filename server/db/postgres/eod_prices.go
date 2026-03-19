package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// ListPrices implements db.EODPriceListDB.
func (p *Postgres) ListPrices(ctx context.Context, search string, dateFrom, dateTo time.Time, dataProvider string, pageSize int32, pageToken string) ([]db.EODPriceRow, int32, string, error) {
	offset := decodePageToken(pageToken)
	displayName := instrumentDisplayNameSQL("i")

	var conditions []string
	var args []interface{}
	argIdx := 1

	if search != "" {
		conditions = append(conditions, fmt.Sprintf(`(%s) ILIKE '%%' || $%d || '%%'`, displayName, argIdx))
		args = append(args, search)
		argIdx++
	}
	if !dateFrom.IsZero() {
		conditions = append(conditions, fmt.Sprintf(`ep.price_date >= $%d`, argIdx))
		args = append(args, dateFrom)
		argIdx++
	}
	if !dateTo.IsZero() {
		conditions = append(conditions, fmt.Sprintf(`ep.price_date <= $%d`, argIdx))
		args = append(args, dateTo)
		argIdx++
	}
	if dataProvider != "" {
		conditions = append(conditions, fmt.Sprintf(`ep.data_provider = $%d`, argIdx))
		args = append(args, dataProvider)
		argIdx++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total matching rows.
	var total int32
	countQ := fmt.Sprintf(`SELECT COUNT(*) FROM eod_prices ep JOIN instruments i ON i.id = ep.instrument_id %s`, where)
	if err := p.q.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count prices: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	q := fmt.Sprintf(`
		SELECT ep.instrument_id, (%s) AS display_name,
			ep.price_date, ep.open, ep.high, ep.low, ep.close, ep.adjusted_close,
			ep.volume, ep.data_provider, ep.fetched_at
		FROM eod_prices ep
		JOIN instruments i ON i.id = ep.instrument_id
		%s
		ORDER BY ep.price_date DESC, lower((%s))
		LIMIT $%d OFFSET $%d
	`, displayName, where, displayName, argIdx, argIdx+1)
	args = append(args, pageSize+1, offset)

	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, "", fmt.Errorf("list prices: %w", err)
	}
	defer rows.Close()

	var results []db.EODPriceRow
	for rows.Next() {
		var r db.EODPriceRow
		var open, high, low, adjClose sql.NullFloat64
		var volume sql.NullInt64
		if err := rows.Scan(
			&r.InstrumentID, &r.InstrumentDisplayName,
			&r.PriceDate, &open, &high, &low, &r.Close, &adjClose,
			&volume, &r.DataProvider, &r.FetchedAt,
		); err != nil {
			return nil, 0, "", err
		}
		if open.Valid {
			r.Open = &open.Float64
		}
		if high.Valid {
			r.High = &high.Float64
		}
		if low.Valid {
			r.Low = &low.Float64
		}
		if adjClose.Valid {
			r.AdjustedClose = &adjClose.Float64
		}
		if volume.Valid {
			r.Volume = &volume.Int64
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}

	var nextToken string
	if int32(len(results)) > pageSize {
		results = results[:pageSize]
		nextToken = encodePageToken(offset + int64(pageSize))
	}

	return results, total, nextToken, nil
}
