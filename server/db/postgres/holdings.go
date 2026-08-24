package postgres

import (
	"context"
	"fmt"
	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

// holdingClosedTest drops the holdings that are closed. It reads the
// split-adjusted sum because that is the only one denominated in a single share
// count: raw quantity is in its own row's share_count_basis, so a buy recorded
// before a split and a sell recorded after it are in different units, and their
// sum is neither zero when the position closed nor the position when it did not.
//
// The adjusted column is exact only to its declared rounding scale, so the test
// is bounded rather than an equality against zero, by the count of rows that may
// have rounded. See qty_is_zero in the schema.
const holdingClosedTest = `HAVING NOT qty_is_zero(
		SUM(t.split_adjusted_quantity),
		COUNT(*) FILTER (WHERE t.split_adjusted_quantity <> t.quantity)::int)`

// A holding is per currency line, not per security: two lines of one security are
// two holdings an FX rate apart, and adding them would report a number in no
// currency at all. A security whose line no posting named groups under the null
// line, which reports unpriced. See
// docs/adr/0072-a-posting-names-a-security-and-a-line.md.

// ComputeHoldings implements db.HoldingsDB.
func (p *Postgres) ComputeHoldings(ctx context.Context, userID string, broker *typev1.Broker, account string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid user id: %w", err)
	}
	asOfT := time.Now().UTC()
	if asOf != nil && asOf.IsValid() {
		asOfT = asOf.AsTime()
	}
	qb := psql.Select("t.broker", "t.account",
		"COALESCE(i.name, MAX(t.instrument_description)) AS instrument_description",
		"t.instrument_id",
		"t.listing_id",
		"COALESCE(l.currency, '') AS currency",
		"SUM(t.split_adjusted_quantity) AS split_adjusted_quantity").
		From("txs t").
		LeftJoin("instruments i ON i.id = t.instrument_id").
		LeftJoin("instrument_listings l ON l.id = t.listing_id").
		Where(sq.Eq{"t.user_id": userUUID}).
		// Only the user's own postings are positions. The non-asset legs (income,
		// charges, residuals, in-flight transfers) share the broker account and
		// would otherwise net against the holding they belong to.
		Where(sq.Eq{"t.account_type": "USER"}).
		Where(sq.LtOrEq{"t.order_date": asOfT}).
		GroupBy("t.broker", "t.account", "t.instrument_id", "t.listing_id", "i.name", "l.currency").
		Suffix(holdingClosedTest)
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, nil, err
		}
		qb = qb.Where(sq.Eq{"t.broker": brokerStr})
	}
	if account != "" {
		qb = qb.Where(sq.Eq{"t.account": account})
	}
	q, args, err := qb.ToSql()
	if err != nil {
		return nil, nil, fmt.Errorf("build compute holdings query: %w", err)
	}
	var hrows []holdingRow
	if err := p.q.SelectContext(ctx, &hrows, q, args...); err != nil {
		return nil, nil, fmt.Errorf("compute holdings: %w", err)
	}
	out := make([]*apiv1.Holding, len(hrows))
	for i := range hrows {
		out[i] = hrows[i].toProto()
	}
	return out, timeToTs(asOfT), nil
}

// ComputeHoldingsForPortfolio implements db.HoldingsDB. Returns holdings for txs matching the portfolio's filters (OR), deduped.
func (p *Postgres) ComputeHoldingsForPortfolio(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	asOfT := time.Now().UTC()
	if asOf != nil && asOf.IsValid() {
		asOfT = asOf.AsTime()
	}
	var hrows []holdingRow
	err = p.q.SelectContext(ctx, &hrows, `
		SELECT t.broker, t.account,
			COALESCE(i.name, MAX(t.instrument_description)) AS instrument_description,
			t.instrument_id,
			t.listing_id,
			COALESCE(l.currency, '') AS currency,
			SUM(t.split_adjusted_quantity) AS split_adjusted_quantity
		FROM txs t
		INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
		LEFT JOIN instruments i ON i.id = t.instrument_id
		LEFT JOIN instrument_listings l ON l.id = t.listing_id
		WHERE t.order_date <= $2 AND t.account_type = 'USER'
		GROUP BY t.broker, t.account, t.instrument_id, t.listing_id, i.name, l.currency
		`+holdingClosedTest, portUUID, asOfT)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings for portfolio: %w", err)
	}
	out := make([]*apiv1.Holding, len(hrows))
	for i := range hrows {
		out[i] = hrows[i].toProto()
	}
	return out, timeToTs(asOfT), nil
}

// CountUnattributedHoldings implements db.HoldingsDB.
//
// The unit is the holding rather than the posting: a position is what a person
// repairs, and counting postings would report one security bought in twenty
// instalments as twenty problems. It is closed the same way the holdings query
// closes one, so a position sold down to nothing does not sit on the dashboard
// forever.
//
// Cash never reaches this. A cash instrument resolves through a CURRENCY
// identifier, so it always has exactly one line and its postings always name it.
func (p *Postgres) CountUnattributedHoldings(ctx context.Context) (int32, int32, error) {
	q := `
		WITH unattributed AS (
			SELECT t.instrument_id
			FROM txs t
			WHERE t.listing_id IS NULL
			  AND t.instrument_id IS NOT NULL
			  AND t.account_type = 'USER'
			GROUP BY t.user_id, t.broker, t.account, t.instrument_id
			` + holdingClosedTest + `
		)
		SELECT
			COUNT(*) FILTER (WHERE EXISTS (
				SELECT 1 FROM instrument_listings l WHERE l.instrument_id = u.instrument_id))::int
				AS no_line_named,
			COUNT(*) FILTER (WHERE NOT EXISTS (
				SELECT 1 FROM instrument_listings l WHERE l.instrument_id = u.instrument_id))::int
				AS no_currency_known
		FROM unattributed u`
	var counts struct {
		NoLineNamed     int32 `db:"no_line_named"`
		NoCurrencyKnown int32 `db:"no_currency_known"`
	}
	if err := p.q.GetContext(ctx, &counts, q); err != nil {
		return 0, 0, fmt.Errorf("count unattributed holdings: %w", err)
	}
	return counts.NoLineNamed, counts.NoCurrencyKnown, nil
}
