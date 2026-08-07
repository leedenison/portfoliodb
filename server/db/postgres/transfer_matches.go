package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/db"
)

// unmatchedTransferSidesSQL reads the TRANSFER_CLEARING residuals no match names,
// each carrying the evidence its whole group holds. $1 is a user id, or the empty
// string for every user.
//
// The evidence is aggregated here rather than stored on the residual. A routed
// posting is arithmetic on the legs supplied, so stamping it with a broker's
// reference would make a derived row indistinguishable from a transcribed one; and
// a group carries a set of references, of which the residual could only carry one.
// The two sides of the sample's lump-sum transfer are refs 545/546/548 against 547,
// where the nearest over the set is 1 and the first leg's alone is 2 -- and it is
// the nearest that decides a match. The counterparty has the same problem from the
// other direction: it sits on whichever leg the source named it on, which need not
// be the leg a residual is built from.
//
// A group whose residual is in a security and one whose residual is money are
// separate rows, because balancing emits one residual per commodity and a match is
// keyed on the pair.
//
// Only a group that produced a TRANSFER_CLEARING residual is read, which is also
// what keeps a counterparty that is not one out of the matcher's reach: Fidelity
// names the product account a service fee was charged for in the same field, but a
// service fee is an INVEXPENSE whose group balances against an EXPENSE leg and
// routes nothing to clearing.
const unmatchedTransferSidesSQL = `
	SELECT t.user_id, t.group_id, t.broker, t.account, t.instrument_id, t.tx_type,
		-- The split-adjusted column, not the raw one: money never splits and the two
		-- agree for it, but the two sides of a securities journal recorded either
		-- side of a split are in different denominations and would not cancel.
		t.split_adjusted_quantity AS amount,
		t.timestamp,
		COALESCE(e.refs, '{}')           AS broker_refs,
		COALESCE(e.counterparties, '{}') AS counterparty_accounts
	FROM txs t
	LEFT JOIN LATERAL (
		SELECT array_agg(DISTINCT p.broker_ref)
			FILTER (WHERE p.broker_ref IS NOT NULL) AS refs,
		       array_agg(DISTINCT p.counterparty_account)
			FILTER (WHERE p.counterparty_account IS NOT NULL) AS counterparties
		FROM txs p
		WHERE p.group_id = t.group_id
	) e ON true
	WHERE t.account_type = 'TRANSFER_CLEARING'
		-- Routing drops a residual it cannot resolve an instrument for, so this
		-- excludes nothing in practice; it is here so a NULL commodity cannot reach
		-- the scan, and because instrument_id is half of a match's key.
		AND t.instrument_id IS NOT NULL
		AND ($1 = '' OR t.user_id = $1::uuid)
		AND NOT EXISTS (
			SELECT 1 FROM transfer_matches m
			WHERE m.instrument_id = t.instrument_id
				AND (m.from_group_id = t.group_id OR m.to_group_id = t.group_id)
		)
	ORDER BY t.user_id, t.instrument_id, t.timestamp, t.group_id`

// transferSideRow is the sqlx-scannable shape for one unmatched side.
type transferSideRow struct {
	UserID               string          `db:"user_id"`
	GroupID              string          `db:"group_id"`
	Broker               string          `db:"broker"`
	Account              string          `db:"account"`
	InstrumentID         string          `db:"instrument_id"`
	TxType               string          `db:"tx_type"`
	Amount               decimal.Decimal `db:"amount"`
	Timestamp            time.Time       `db:"timestamp"`
	BrokerRefs           pq.StringArray  `db:"broker_refs"`
	CounterpartyAccounts pq.StringArray  `db:"counterparty_accounts"`
}

func (r *transferSideRow) toDomain() db.TransferSide {
	return db.TransferSide{
		UserID:               r.UserID,
		GroupID:              r.GroupID,
		Broker:               strToBroker(r.Broker),
		Account:              r.Account,
		InstrumentID:         r.InstrumentID,
		TxType:               strToTxType(r.TxType),
		Amount:               r.Amount,
		Timestamp:            r.Timestamp,
		BrokerRefs:           []string(r.BrokerRefs),
		CounterpartyAccounts: []string(r.CounterpartyAccounts),
	}
}

// ListUnmatchedTransferSides implements db.TransferMatchDB.
func (p *Postgres) ListUnmatchedTransferSides(ctx context.Context, opts db.TransferSideOpts) ([]db.TransferSide, error) {
	var rows []transferSideRow
	if err := p.q.SelectContext(ctx, &rows, unmatchedTransferSidesSQL, opts.UserID); err != nil {
		return nil, fmt.Errorf("list unmatched transfer sides: %w", err)
	}
	out := make([]db.TransferSide, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

// insertTransferMatchSQL writes one link unless either side is already matched in
// that commodity.
//
// A guarded insert rather than ON CONFLICT, which can infer only one index and there
// are two -- one per direction. The guard also covers the case the indexes cannot:
// a group arriving as the *from* side of one proposed link and the *to* side of
// another within the same batch.
//
// This is what makes a matching pass re-runnable. A cycle over unchanged data
// proposes the links that already exist and writes none of them.
const insertTransferMatchSQL = `
	INSERT INTO transfer_matches (user_id, from_group_id, to_group_id, instrument_id, method)
	SELECT $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5
	WHERE NOT EXISTS (
		SELECT 1 FROM transfer_matches m
		WHERE m.instrument_id = $4::uuid
			AND (m.from_group_id IN ($2::uuid, $3::uuid)
			  OR m.to_group_id   IN ($2::uuid, $3::uuid))
	)`

// CreateTransferMatches implements db.TransferMatchDB.
func (p *Postgres) CreateTransferMatches(ctx context.Context, matches []db.TransferMatch) (int, error) {
	if len(matches) == 0 {
		return 0, nil
	}
	written := 0
	err := p.runInTx(ctx, func(exec queryable) error {
		for _, m := range matches {
			res, err := exec.ExecContext(ctx, insertTransferMatchSQL,
				m.UserID, m.FromGroupID, m.ToGroupID, m.InstrumentID, m.Method)
			if err != nil {
				return fmt.Errorf("insert transfer match: %w", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("insert transfer match: %w", err)
			}
			written += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// ListTransferMatches implements db.TransferMatchDB.
func (p *Postgres) ListTransferMatches(ctx context.Context, userID string) ([]db.TransferMatch, error) {
	var rows []struct {
		UserID       string `db:"user_id"`
		FromGroupID  string `db:"from_group_id"`
		ToGroupID    string `db:"to_group_id"`
		InstrumentID string `db:"instrument_id"`
		Method       string `db:"method"`
	}
	q := `
		SELECT user_id, from_group_id, to_group_id, instrument_id, method
		FROM transfer_matches WHERE user_id = $1::uuid
		ORDER BY matched_at DESC, id`
	if err := p.q.SelectContext(ctx, &rows, q, userID); err != nil {
		return nil, fmt.Errorf("list transfer matches: %w", err)
	}
	out := make([]db.TransferMatch, len(rows))
	for i, r := range rows {
		out[i] = db.TransferMatch{
			UserID:       r.UserID,
			FromGroupID:  r.FromGroupID,
			ToGroupID:    r.ToGroupID,
			InstrumentID: r.InstrumentID,
			Method:       r.Method,
		}
	}
	return out, nil
}
