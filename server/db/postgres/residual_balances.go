package postgres

import (
	"context"
	"fmt"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

// residualBalanceAgg aggregates the residual postings -- the IMBALANCE,
// TRANSFER_CLEARING and SOURCE_ROUNDING legs routed to balance groups whose source
// data was one-sided or disagreed with itself -- across every user. $1/$2 are a
// half-open [from, before) window over the posting timestamp, either of which may be
// NULL for an open bound; $3 restricts to one account type, or is the empty string
// for all of them.
//
// SOURCE_ROUNDING is reported rather than hidden: one such posting is noise, but the
// aggregate says how much a broker's rounding costs and over how many postings, which
// is the only place that is visible. It is deliberately absent from the dashboard
// counts below, which ask whether something is wrong.
//
// The commodity is the instrument's name, not its currency: for money the two
// agree, but a residual left by an unpaired JRNLSEC is a quantity of shares whose
// instrument currency would render it as money and misstate it.
//
// Both sides of a completed journal are TRANSFER_CLEARING postings in different
// broker accounts, and nothing pairs them: matching is 0068. Until it lands a
// settled transfer is indistinguishable from an unmatched one and both are
// reported, so a TRANSFER_CLEARING balance says a transfer was imported, not that
// a side is missing. The list and the count share this text so the two cannot
// disagree about what a balance is.
const residualBalanceAgg = `
	SELECT t.account_type, t.broker, t.account, t.instrument_id,
		COALESCE(i.name, t.instrument_id::text) AS commodity,
		COALESCE(i.asset_class, '')             AS asset_class,
		t.tx_type,
		-- The split-adjusted column, not the raw one: money never splits and the
		-- two agree for it, but a residual left by an unpaired JRNLSEC is a
		-- quantity of shares, and raw quantities recorded either side of a split
		-- are in different denominations and do not sum. See qty_is_zero.
		SUM(t.split_adjusted_quantity) AS balance,
		COUNT(*)::int     AS posting_count,
		MIN(t.timestamp)  AS oldest,
		MAX(t.timestamp)  AS newest
	FROM txs t
	LEFT JOIN instruments i ON i.id = t.instrument_id
	WHERE t.account_type IN ('IMBALANCE', 'TRANSFER_CLEARING', 'SOURCE_ROUNDING')
		-- Routing drops a residual it cannot resolve an instrument for, so this
		-- excludes nothing in practice; it is here so a NULL commodity cannot
		-- reach the scan.
		AND t.instrument_id IS NOT NULL
		AND ($1::timestamptz IS NULL OR t.timestamp >= $1)
		AND ($2::timestamptz IS NULL OR t.timestamp <  $2)
		AND ($3 = '' OR t.account_type = $3)
	GROUP BY t.account_type, t.broker, t.account, t.instrument_id, i.name, i.asset_class, t.tx_type
	HAVING NOT qty_is_zero(
		SUM(t.split_adjusted_quantity),
		COUNT(*) FILTER (WHERE t.split_adjusted_quantity <> t.quantity)::int)`

// residualBalanceRow is the sqlx-scannable shape for a residual balance.
type residualBalanceRow struct {
	AccountType  string          `db:"account_type"`
	Broker       string          `db:"broker"`
	Account      string          `db:"account"`
	InstID       *string         `db:"instrument_id"`
	Commodity    string          `db:"commodity"`
	AssetClass   string          `db:"asset_class"`
	TxType       string          `db:"tx_type"`
	Balance      decimal.Decimal `db:"balance"`
	PostingCount int32           `db:"posting_count"`
	Oldest       *time.Time      `db:"oldest"`
	Newest       *time.Time      `db:"newest"`
}

func (r *residualBalanceRow) toDomain() db.ResidualBalance {
	b := db.ResidualBalance{
		AccountType:  strToAccountType(r.AccountType),
		Broker:       strToBroker(r.Broker),
		Account:      r.Account,
		Commodity:    r.Commodity,
		AssetClass:   r.AssetClass,
		TxType:       strToTxType(r.TxType),
		Balance:      r.Balance,
		PostingCount: r.PostingCount,
		Oldest:       r.Oldest,
		Newest:       r.Newest,
	}
	if r.InstID != nil {
		b.InstrumentID = *r.InstID
	}
	return b
}

// ListResidualBalances implements db.ResidualBalanceDB.
func (p *Postgres) ListResidualBalances(ctx context.Context, opts db.ResidualBalanceOpts) ([]db.ResidualBalance, error) {
	accountType := ""
	if opts.AccountType != apiv1.AccountType_ACCOUNT_TYPE_UNSPECIFIED {
		s, err := accountTypeToStr(opts.AccountType)
		if err != nil {
			return nil, err
		}
		accountType = s
	}
	q := residualBalanceAgg + `
		ORDER BY t.broker, t.account, commodity, t.tx_type`
	var rows []residualBalanceRow
	if err := p.q.SelectContext(ctx, &rows, q, opts.From, opts.Before, accountType); err != nil {
		return nil, fmt.Errorf("list residual balances: %w", err)
	}
	out := make([]db.ResidualBalance, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

// CountResidualBalances implements db.ResidualBalanceDB. It counts over all of
// history: the dashboard asks whether anything is wrong, not what was wrong during
// some window. SOURCE_ROUNDING is not counted -- the source rounding its own cash
// figures is not a fault, and counting it would make the dashboard permanently red.
func (p *Postgres) CountResidualBalances(ctx context.Context, staleBefore time.Time) (int32, int32, error) {
	q := "WITH r AS (" + residualBalanceAgg + `
		)
		SELECT
			COUNT(*) FILTER (WHERE r.account_type = 'IMBALANCE')::int AS imbalances,
			COUNT(*) FILTER (WHERE r.account_type = 'TRANSFER_CLEARING'
				AND COALESCE(r.oldest, r.newest) < $4)::int AS stale_transfers
		FROM r`
	var counts struct {
		Imbalances     int32 `db:"imbalances"`
		StaleTransfers int32 `db:"stale_transfers"`
	}
	if err := p.q.GetContext(ctx, &counts, q, nil, nil, "", staleBefore); err != nil {
		return 0, 0, fmt.Errorf("count residual balances: %w", err)
	}
	return counts.Imbalances, counts.StaleTransfers, nil
}
