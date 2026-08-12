package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/db"
)

// groupingColumns is what the engine's rules compare, plus the group as stored so a
// cycle can tell its own answer from what is there.
//
// The raw quantity and price, never the split-adjusted pair: a rule matches figures
// the source wrote against each other, and the adjusted pair carries a rounding the
// source never had.
const groupingColumns = `
	t.id::text AS id,
	t.user_id::text AS user_id,
	t.broker,
	t.account,
	t.timestamp,
	COALESCE(t.instrument_id::text, '') AS instrument_id,
	t.quantity,
	t.unit_price,
	t.settlement_amount,
	t.broker_tx_type,
	COALESCE(g.job_id::text, '') AS job_id,
	t.group_id::text AS group_id,
	t.resolved_tx_type
`

// transcribedPostings is the population a partition is over.
//
// A routed residual transcribes nothing and correlates with nothing, so there is no
// evidence to say which postings it belongs with; it is deleted and routed fresh once
// the partition is settled. A group holding a synthetic pad is left alone whole --
// the pad and its EQUITY counterparty are the declaration machinery's, and half a
// group is not a group.
//
// The pad is named rather than inferred from a purpose being present. A residual now
// carries one too, so "the group holds something the server made" no longer picks out
// the pad: a group is excluded because it holds a declaration's pad, and a residual is
// excluded because it is not a posting anything could partition.
//
// The account type is not read. A leg a converter read out of a record lands outside
// the user's own account -- the income a reinvestment consumed is the case -- and it
// has to be partitioned like any other stated posting, or the record it came from
// could never be reassembled. What that leg carries and a derived one does not is
// evidence, and synthetic_purpose is what says which is which.
const transcribedPostings = `
	FROM txs t
	JOIN tx_groups g ON g.id = t.group_id
	WHERE t.user_id = $1::uuid
	  AND t.synthetic_purpose IS NULL
	  AND NOT EXISTS (
	    SELECT 1 FROM txs s
	    WHERE s.group_id = t.group_id AND s.synthetic_purpose = '` + db.InitializePurpose + `'
	  )
`

// anyUsersPostings is transcribedPostings with the user optional, for the seed read:
// a cycle triggered by an import knows whose data it wrote, and one on a cadence
// sweeps every user.
var anyUsersPostings = strings.Replace(transcribedPostings,
	"WHERE t.user_id = $1::uuid",
	"WHERE ($1 = '' OR t.user_id = $1::uuid)", 1)

// groupingRow is the scan shape for a posting the engine reads.
type groupingRow struct {
	ID               string           `db:"id"`
	UserID           string           `db:"user_id"`
	Broker           string           `db:"broker"`
	Account          string           `db:"account"`
	Timestamp        time.Time        `db:"timestamp"`
	InstrumentID     string           `db:"instrument_id"`
	Quantity         decimal.Decimal  `db:"quantity"`
	UnitPrice        *decimal.Decimal `db:"unit_price"`
	SettlementAmount *decimal.Decimal `db:"settlement_amount"`
	BrokerTxTypes    pq.StringArray   `db:"broker_tx_type"`
	JobID            string           `db:"job_id"`
	GroupID          string           `db:"group_id"`
	Resolved         string           `db:"resolved_tx_type"`
}

func (r groupingRow) toDomain() db.GroupingPosting {
	return db.GroupingPosting{
		ID:               r.ID,
		UserID:           r.UserID,
		Broker:           db.StrToBroker(r.Broker),
		Account:          r.Account,
		Timestamp:        r.Timestamp,
		InstrumentID:     r.InstrumentID,
		Quantity:         r.Quantity,
		UnitPrice:        r.UnitPrice,
		SettlementAmount: r.SettlementAmount,
		Declared:         db.StrsToTxTypes(r.BrokerTxTypes),
		JobID:            r.JobID,
		GroupID:          r.GroupID,
		Resolved:         r.Resolved,
	}
}

// ListGroupingSeeds implements db.GroupingDB.
func (p *Postgres) ListGroupingSeeds(ctx context.Context, opts db.GroupingSeedOpts) ([]db.GroupingPosting, error) {
	if opts.UserID != "" {
		if _, err := uuid.Parse(opts.UserID); err != nil {
			return nil, fmt.Errorf("invalid user id: %w", err)
		}
	}
	args := []interface{}{opts.UserID}
	q := "SELECT" + groupingColumns + anyUsersPostings
	if opts.Residual {
		// The partial index over residual postings answers this directly. Source
		// rounding is excluded: it is the source disagreeing with itself rather
		// than a leg something is missing, so a group carrying only that is not a
		// reason to go looking.
		q += `
		  AND EXISTS (
		    SELECT 1 FROM txs r
		    WHERE r.group_id = t.group_id
		      AND r.account_type IN ('IMBALANCE', 'TRANSFER_CLEARING')
		  )`
	}
	if opts.JobID != "" {
		jobUUID, err := uuid.Parse(opts.JobID)
		if err != nil {
			return nil, fmt.Errorf("invalid job id: %w", err)
		}
		args = append(args, jobUUID)
		q += fmt.Sprintf("\n\t\t  AND g.job_id = $%d", len(args))
	}
	q += "\n\t\tORDER BY t.user_id, t.id"
	return p.groupingPostings(ctx, q, args)
}

// notHeld excludes the ids the caller already has.
//
// COALESCE because an empty list arrives as SQL NULL, and every comparison against
// NULL is NULL -- so the predicate meant to exclude nothing would reject every row.
func notHeld(n int) string {
	return fmt.Sprintf("\n\t\t  AND NOT (t.id::text = ANY(COALESCE($%d::text[], ARRAY[]::text[])))\n\t\tORDER BY t.id", n)
}

// PostingsByToken implements db.GroupingReader.
//
// The index over (label, token) answers this directly, which is the lookup a
// grouping rule makes: it starts from an identifier and asks who else holds it. A
// query whose scope reaches past the account that issued the token is looked for past
// it, or the read would be narrower than the rule it serves.
func (p *Postgres) PostingsByToken(ctx context.Context, userID string, qs []db.TokenQuery, held []string) ([]db.GroupingPosting, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	if len(qs) == 0 {
		return nil, nil
	}
	labels, tokens, brokers, accounts := make([]string, len(qs)), make([]string, len(qs)), make([]string, len(qs)), make([]string, len(qs))
	anyAccount := make([]bool, len(qs))
	for i, q := range qs {
		labels[i], tokens[i] = q.Label, q.Token
		brokers[i], accounts[i] = db.BrokerToStr(q.Broker), q.Account
		anyAccount[i] = q.AnyAccount
	}
	sql := "SELECT" + groupingColumns + transcribedPostings + `
		  AND EXISTS (
		    SELECT 1
		    FROM tx_correlations c
		    JOIN unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::bool[])
		      AS r(label, token, broker, account, any_account)
		      ON c.label = r.label AND c.token = r.token
		    WHERE c.tx_id = t.id
		      AND t.broker = r.broker
		      AND (r.any_account OR t.account = r.account)
		  )` + notHeld(7)
	return p.groupingPostings(ctx, sql, []interface{}{
		userUUID, pq.Array(labels), pq.Array(tokens), pq.Array(brokers),
		pq.Array(accounts), pq.Array(anyAccount), pq.Array(held),
	})
}

// PostingsByDates implements db.GroupingReader, over the account-and-day bucket the
// trade rules pair within.
func (p *Postgres) PostingsByDates(ctx context.Context, userID string, qs []db.DateQuery, held []string) ([]db.GroupingPosting, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	if len(qs) == 0 {
		return nil, nil
	}
	brokers, accounts := make([]string, len(qs)), make([]string, len(qs))
	from, before := make([]time.Time, len(qs)), make([]time.Time, len(qs))
	for i, q := range qs {
		brokers[i], accounts[i] = db.BrokerToStr(q.Broker), q.Account
		from[i], before[i] = q.From, q.Before
	}
	sql := "SELECT" + groupingColumns + transcribedPostings + `
		  AND EXISTS (
		    SELECT 1
		    FROM unnest($2::text[], $3::text[], $4::timestamptz[], $5::timestamptz[])
		      AS r(broker, account, from_ts, before_ts)
		    WHERE t.broker = r.broker AND t.account = r.account
		      AND t.timestamp >= r.from_ts AND t.timestamp < r.before_ts
		  )` + notHeld(6)
	return p.groupingPostings(ctx, sql, []interface{}{
		userUUID, pq.Array(brokers), pq.Array(accounts), pq.Array(from), pq.Array(before), pq.Array(held),
	})
}

// PostingsByOrdinals implements db.GroupingReader, over the reference span a deposit
// run finds its steps within. No date bound: a run can settle across two days.
func (p *Postgres) PostingsByOrdinals(ctx context.Context, userID string, qs []db.OrdinalQuery, held []string) ([]db.GroupingPosting, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	if len(qs) == 0 {
		return nil, nil
	}
	brokers, accounts, labels := make([]string, len(qs)), make([]string, len(qs)), make([]string, len(qs))
	low, high := make([]int64, len(qs)), make([]int64, len(qs))
	for i, q := range qs {
		brokers[i], accounts[i], labels[i] = db.BrokerToStr(q.Broker), q.Account, q.Label
		low[i], high[i] = q.Low, q.High
	}
	sql := "SELECT" + groupingColumns + transcribedPostings + `
		  AND EXISTS (
		    SELECT 1
		    FROM tx_correlations c
		    JOIN unnest($2::text[], $3::text[], $4::text[], $5::bigint[], $6::bigint[])
		      AS r(broker, account, label, lo, hi)
		      ON c.label = r.label
		    WHERE c.tx_id = t.id
		      AND c.ordinal BETWEEN r.lo AND r.hi
		      AND t.broker = r.broker AND t.account = r.account
		  )` + notHeld(7)
	return p.groupingPostings(ctx, sql, []interface{}{
		userUUID, pq.Array(brokers), pq.Array(accounts), pq.Array(labels),
		pq.Array(low), pq.Array(high), pq.Array(held),
	})
}

// groupingPostingsByID reads the postings a write just put down, in the shape a
// partition is derived over, for the store to seed the settler with.
//
// It goes through transcribedPostings like every other grouping read, so a routed
// leg or a group holding a declaration's pad is never offered as a seed. It takes an
// exec rather than reading p.q because the postings it names are not committed yet:
// the whole point is to partition them in the transaction that wrote them.
func groupingPostingsByID(ctx context.Context, exec queryable, userUUID uuid.UUID, ids []uuid.UUID) ([]db.GroupingPosting, error) {
	q := "SELECT" + groupingColumns + transcribedPostings + `
		  AND t.id = ANY($2::uuid[])
		ORDER BY t.id`
	return NewWithQueryable(exec).groupingPostings(ctx, q, []interface{}{userUUID, pq.Array(ids)})
}

// groupingPostings runs one of the reads above and hangs each posting's correlations
// on it.
//
// A second pass for the evidence rather than a lateral aggregate, as the export path
// does: the postings come back as one ordered stream, and a document per row would be
// decoded whether the row correlates with anything or not.
func (p *Postgres) groupingPostings(ctx context.Context, q string, args []interface{}) ([]db.GroupingPosting, error) {
	var rows []groupingRow
	if err := p.q.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("list postings for grouping: %w", err)
	}
	ids := make([]string, len(rows))
	out := make([]db.GroupingPosting, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		out[i] = r.toDomain()
	}
	byTx, err := p.correlationsByTx(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Correlations = byTx[out[i].ID]
	}
	return out, nil
}
