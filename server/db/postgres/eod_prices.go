package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
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
		"ep.volume", "ep.data_provider", "ep.last_fetched_at",
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
		var open, high, low, adjClose decimal.NullDecimal
		var volume sql.NullInt64
		if err := rows.Scan(
			&r.InstrumentID, &r.InstrumentDisplayName,
			&r.PriceDate, &open, &high, &low, &r.Close, &adjClose,
			&volume, &r.DataProvider, &r.LastFetchedAt, &r.ShareCountBasis,
		); err != nil {
			return nil, 0, "", err
		}
		if open.Valid {
			r.Open = &open.Decimal
		}
		if high.Valid {
			r.High = &high.Decimal
		}
		if low.Valid {
			r.Low = &low.Decimal
		}
		if adjClose.Valid {
			r.AdjustedClose = &adjClose.Decimal
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
	db.InstrumentRef
	AssetClass      string           `db:"asset_class"`
	Currency        string           `db:"currency"`
	PriceDate       time.Time        `db:"price_date"`
	ShareCountBasis *time.Time       `db:"share_count_basis"`
	Open            *decimal.Decimal `db:"open"`
	High            *decimal.Decimal `db:"high"`
	Low             *decimal.Decimal `db:"low"`
	Close           decimal.Decimal  `db:"close"`
	AdjustedClose   *decimal.Decimal `db:"adjusted_close"`
	Volume          *int64           `db:"volume"`
}

// ListPricesForExport implements db.EODPriceListDB.
func (p *Postgres) ListPricesForExport(ctx context.Context) ([]db.ExportPriceRow, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			COALESCE(i.currency, '') AS currency,
			ep.price_date, ep.open, ep.high, ep.low, ep.close,
			ep.adjusted_close, ep.volume,
			-- A basis equal to the bar's own date is the as-traded convention
			-- and says nothing a reader cannot infer. The column is NOT NULL
			-- and defaults to price_date, so selecting it raw would stamp a
			-- redundant date onto every bar in the file.
			CASE WHEN ep.share_count_basis = ep.price_date THEN NULL
				ELSE ep.share_count_basis END AS share_count_basis
		FROM eod_prices ep
		JOIN instruments i ON i.id = ep.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), ep.price_date
	`
	var rows []exportPriceRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list prices for export: %w", err)
	}
	out := make([]db.ExportPriceRow, len(rows))
	for i, r := range rows {
		out[i] = db.ExportPriceRow{
			Ref:             r.InstrumentRef,
			AssetClass:      r.AssetClass,
			Currency:        r.Currency,
			PriceDate:       r.PriceDate,
			ShareCountBasis: r.ShareCountBasis,
			Open:            r.Open,
			High:            r.High,
			Low:             r.Low,
			Close:           r.Close,
			AdjustedClose:   r.AdjustedClose,
			Volume:          r.Volume,
		}
	}
	return out, nil
}

// exportPriceCoverageRow is a sqlx-scannable version of db.ExportPriceCoverageRow.
type exportPriceCoverageRow struct {
	db.InstrumentRef
	AssetClass string    `db:"asset_class"`
	Currency   string    `db:"currency"`
	From       time.Time `db:"covered_from"`
	Before     time.Time `db:"covered_before"`
}

// ListPriceCoverageForExport implements db.EODPriceListDB.
//
// Spans come from price_coverage, so a range a provider answered with no bars
// travels with the file. Merged across plugins: an import stores everything
// under one provider, so the distinction cannot survive the round trip.
//
// The asset class and currency come along because an instrument can be covered
// and have no rows, and then this query is the only place its group can get
// them.
func (p *Postgres) ListPriceCoverageForExport(ctx context.Context) ([]db.ExportPriceCoverageRow, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			COALESCE(i.currency, '') AS currency,
			mc.covered_from, mc.covered_before
		FROM merged_price_coverage mc
		JOIN instruments i ON i.id = mc.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), covered_from
	`
	var rows []exportPriceCoverageRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list price coverage for export: %w", err)
	}
	out := make([]db.ExportPriceCoverageRow, len(rows))
	for i, r := range rows {
		out[i] = db.ExportPriceCoverageRow{
			Ref:        r.InstrumentRef,
			AssetClass: r.AssetClass,
			Currency:   r.Currency,
			From:       r.From,
			Before:     r.Before,
		}
	}
	return out, nil
}
