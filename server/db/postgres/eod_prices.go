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
func (p *Postgres) ListPrices(ctx context.Context, search string, dateFrom, dateBefore time.Time, dataProvider string, pageSize int32, pageToken string) ([]db.EODPriceRow, int32, string, error) {
	offset := decodePageToken(pageToken)

	// Build shared WHERE conditions for count and data queries.
	where := sq.And{}
	if search != "" {
		where = append(where, sq.ILike{"i.name": "%" + search + "%"})
	}
	if !dateFrom.IsZero() {
		where = append(where, sq.GtOrEq{"ep.price_date": dateFrom})
	}
	if !dateBefore.IsZero() {
		where = append(where, sq.Lt{"ep.price_date": dateBefore})
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
		"ep.volume", "ep.data_provider", "ep.synthetic", "ep.last_fetched_at",
		"ep.share_count_basis",
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
			&volume, &r.DataProvider, &r.Synthetic, &r.LastFetchedAt, &r.ShareCountBasis,
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
	Currency         string    `db:"currency"`
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
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			COALESCE(i.currency, '') AS currency,
			ep.price_date, ep.open, ep.high, ep.low, ep.close,
			ep.adjusted_close, ep.volume
		FROM eod_prices ep
		JOIN instruments i ON i.id = ep.instrument_id
		` + bestIdentifierJoin + `
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
			Currency:         r.Currency,
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

// exportCoverageRow is a sqlx-scannable version of db.ExportCoverageRow.
type exportCoverageRow struct {
	IdentifierType   string    `db:"identifier_type"`
	IdentifierValue  string    `db:"value"`
	IdentifierDomain string    `db:"domain"`
	From             time.Time `db:"covered_from"`
	Before           time.Time `db:"covered_before"`
}

func toExportCoverageRows(rows []exportCoverageRow) []db.ExportCoverageRow {
	out := make([]db.ExportCoverageRow, len(rows))
	for i, r := range rows {
		out[i] = db.ExportCoverageRow{
			IdentifierType:   r.IdentifierType,
			IdentifierValue:  r.IdentifierValue,
			IdentifierDomain: r.IdentifierDomain,
			From:             r.From,
			Before:           r.Before,
		}
	}
	return out
}

// ListPriceCoverageForExport implements db.EODPriceListDB.
//
// Synthetic rows are included in the aggregation even though
// ListPricesForExport omits them: the span is exactly what tells an import
// which days to regenerate.
func (p *Postgres) ListPriceCoverageForExport(ctx context.Context) ([]db.ExportCoverageRow, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			lower(sub.r) AS covered_from, upper(sub.r) AS covered_before
		FROM (
			SELECT instrument_id,
				unnest(range_agg(daterange(price_date, price_date + 1))) AS r
			FROM eod_prices
			GROUP BY instrument_id
		) sub
		JOIN instruments i ON i.id = sub.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, covered_from
	`
	var rows []exportCoverageRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list price coverage for export: %w", err)
	}
	return toExportCoverageRows(rows), nil
}
