package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	SELECT t.user_id, t.group_id, t.broker, t.account, t.instrument_id,
		-- The split-adjusted column, not the raw one: money never splits and the two
		-- agree for it, but the two sides of a securities journal recorded either
		-- side of a split are in different denominations and would not cancel.
		t.split_adjusted_quantity AS amount,
		t.timestamp,
		COALESCE(e.correlations, '[]'::jsonb) AS correlations
	FROM txs t
	-- The evidence of the whole group, not of the clearing leg, which is routed
	-- and transcribes nothing. As JSON because a correlation is a record rather
	-- than a value, and the alternative -- one row per correlation on a scan
	-- that is already one row per side -- would multiply the sides out and make
	-- the caller re-collapse them.
	LEFT JOIN LATERAL (
		SELECT jsonb_agg(DISTINCT jsonb_build_object(
			'token', c.token,
			'ordinal', c.ordinal,
			'match', c.matches
		)) AS correlations
		FROM txs p
		JOIN tx_correlations c ON c.tx_id = p.id
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
//
// Correlations arrive as the JSON the lateral built. Only the three fields a
// matching pass reads are selected: the label partitions series for grouping
// rather than for matching, and the scope is a statement about which postings are
// comparable that the passes already make for themselves -- both are same-broker,
// and a pointer is compared against an account rather than against another token.
type transferSideRow struct {
	UserID       string          `db:"user_id"`
	GroupID      string          `db:"group_id"`
	Broker       string          `db:"broker"`
	Account      string          `db:"account"`
	InstrumentID string          `db:"instrument_id"`
	Amount       decimal.Decimal `db:"amount"`
	Timestamp    time.Time       `db:"timestamp"`
	Correlations []byte          `db:"correlations"`
}

func (r *transferSideRow) toDomain() (db.TransferSide, error) {
	var cs []struct {
		Token   string   `json:"token"`
		Ordinal *int64   `json:"ordinal"`
		Match   []string `json:"match"`
	}
	if err := json.Unmarshal(r.Correlations, &cs); err != nil {
		return db.TransferSide{}, fmt.Errorf("correlations of group %s: %w", r.GroupID, err)
	}
	side := db.TransferSide{
		UserID:       r.UserID,
		GroupID:      r.GroupID,
		Broker:       strToBroker(r.Broker),
		Account:      r.Account,
		InstrumentID: r.InstrumentID,
		Amount:       r.Amount,
		Timestamp:    r.Timestamp,
	}
	for _, c := range cs {
		side.Correlations = append(side.Correlations, db.Correlation{
			Token:   c.Token,
			Ordinal: c.Ordinal,
			Match:   c.Match,
		})
	}
	return side, nil
}

// ListUnmatchedTransferSides implements db.TransferMatchDB.
func (p *Postgres) ListUnmatchedTransferSides(ctx context.Context, opts db.TransferSideOpts) ([]db.TransferSide, error) {
	var rows []transferSideRow
	if err := p.q.SelectContext(ctx, &rows, unmatchedTransferSidesSQL, opts.UserID); err != nil {
		return nil, fmt.Errorf("list unmatched transfer sides: %w", err)
	}
	out := make([]db.TransferSide, len(rows))
	for i := range rows {
		side, err := rows[i].toDomain()
		if err != nil {
			return nil, fmt.Errorf("list unmatched transfer sides: %w", err)
		}
		out[i] = side
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

// transferMatchRow is the sqlx-scannable shape for one link.
type transferMatchRow struct {
	UserID       string `db:"user_id"`
	FromGroupID  string `db:"from_group_id"`
	ToGroupID    string `db:"to_group_id"`
	InstrumentID string `db:"instrument_id"`
	Method       string `db:"method"`
}

func (r *transferMatchRow) toDomain() db.TransferMatch {
	return db.TransferMatch{
		UserID:       r.UserID,
		FromGroupID:  r.FromGroupID,
		ToGroupID:    r.ToGroupID,
		InstrumentID: r.InstrumentID,
		Method:       r.Method,
	}
}

// ListTransferMatches implements db.TransferMatchDB.
func (p *Postgres) ListTransferMatches(ctx context.Context, userID string) ([]db.TransferMatch, error) {
	q := `
		SELECT user_id, from_group_id, to_group_id, instrument_id, method
		FROM transfer_matches WHERE user_id = $1::uuid
		ORDER BY matched_at DESC, id`
	var rows []transferMatchRow
	if err := p.q.SelectContext(ctx, &rows, q, userID); err != nil {
		return nil, fmt.Errorf("list transfer matches: %w", err)
	}
	out := make([]db.TransferMatch, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
