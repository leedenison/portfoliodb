package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/leedenison/portfoliodb/server/db"
)

// ListPrices implements db.EODPriceListDB.
func (p *Postgres) ListPrices(ctx context.Context, search string, dateFrom, dateTo time.Time, dataProvider string, pageSize int32, pageToken string) ([]db.EODPriceRow, int32, string, error) {
	offset := decodePageToken(pageToken)

	// Build shared WHERE conditions for count and data queries.
	where := sq.And{}
	if search != "" {
		where = append(where, sq.ILike{"i.name": "%" + search + "%"})
	}
	if !dateFrom.IsZero() {
		where = append(where, sq.GtOrEq{"ep.price_date": dateFrom})
	}
	if !dateTo.IsZero() {
		where = append(where, sq.LtOrEq{"ep.price_date": dateTo})
	}
	if dataProvider != "" {
		where = append(where, sq.Eq{"ep.data_provider": dataProvider})
	}

	// Count total matching rows.
	countQ, countArgs, err := psql.Select("COUNT(*)").
		From("eod_prices ep").
		Join("instruments i ON i.id = ep.instrument_id").
		Where(where).
		ToSql()
	if err != nil {
		return nil, 0, "", fmt.Errorf("build count prices query: %w", err)
	}
	var total int32
	if err := p.q.QueryRowContext(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count prices: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	q, args, err := psql.Select(
		"ep.instrument_id", "i.name AS display_name",
		"ep.price_date", "ep.open", "ep.high", "ep.low", "ep.close", "ep.adjusted_close",
		"ep.volume", "ep.data_provider", "ep.synthetic", "ep.fetched_at",
	).
		From("eod_prices ep").
		Join("instruments i ON i.id = ep.instrument_id").
		Where(where).
		OrderBy("ep.price_date DESC", "lower(i.name)").
		Limit(uint64(pageSize + 1)).Offset(uint64(offset)).
		ToSql()
	if err != nil {
		return nil, 0, "", fmt.Errorf("build list prices query: %w", err)
	}

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
			&volume, &r.DataProvider, &r.Synthetic, &r.FetchedAt,
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

// exportPriceRow is a sqlx-scannable version of db.ExportPriceRow.
type exportPriceRow struct {
	IdentifierType   string    `db:"identifier_type"`
	IdentifierValue  string    `db:"value"`
	IdentifierDomain string    `db:"domain"`
	AssetClass       string    `db:"asset_class"`
	PriceDate        time.Time `db:"price_date"`
	Open             *float64  `db:"open"`
	High             *float64  `db:"high"`
	Low              *float64  `db:"low"`
	Close            float64   `db:"close"`
	AdjustedClose    *float64  `db:"adjusted_close"`
	Volume           *int64    `db:"volume"`
}

// ListPricesForExport implements db.EODPriceListDB.
func (p *Postgres) ListPricesForExport(ctx context.Context) ([]db.ExportPriceRow, error) {
	const q = `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			ep.price_date, ep.open, ep.high, ep.low, ep.close,
			ep.adjusted_close, ep.volume
		FROM eod_prices ep
		JOIN instruments i ON i.id = ep.instrument_id
		JOIN LATERAL (
			SELECT ii.identifier_type, ii.value, ii.domain
			FROM instrument_identifiers ii
			WHERE ii.instrument_id = ep.instrument_id
			ORDER BY CASE ii.identifier_type
				WHEN 'MIC_TICKER' THEN 1
				WHEN 'OPENFIGI_TICKER' THEN 2
				WHEN 'OCC' THEN 3
				WHEN 'ISIN' THEN 4
				WHEN 'OPENFIGI_GLOBAL' THEN 5
				WHEN 'OPENFIGI_SHARE_CLASS' THEN 6
				WHEN 'OPENFIGI_COMPOSITE' THEN 7
				WHEN 'CUSIP' THEN 8
				WHEN 'SEDOL' THEN 9
				WHEN 'OPRA' THEN 10
				WHEN 'BROKER_DESCRIPTION' THEN 11
				ELSE 99
			END
			LIMIT 1
		) best_id ON true
		WHERE NOT ep.synthetic
		ORDER BY best_id.identifier_type, best_id.value, ep.price_date
	`
	var rows []exportPriceRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list prices for export: %w", err)
	}
	out := make([]db.ExportPriceRow, len(rows))
	for i, r := range rows {
		out[i] = db.ExportPriceRow{
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
			AssetClass:       r.AssetClass,
			PriceDate:        r.PriceDate,
			Open:             r.Open,
			High:             r.High,
			Low:              r.Low,
			Close:            r.Close,
			AdjustedClose:    r.AdjustedClose,
			Volume:           r.Volume,
		}
	}
	return out, nil
}
