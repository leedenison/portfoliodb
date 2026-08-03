package postgres

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// insertPostingSQL inserts a tx as the single posting of a new tx group. The group
// is created in the same statement so that grouping costs no extra round trip.
// split_adjusted_quantity / split_adjusted_unit_price are omitted deliberately:
// default_split_adjusted_tx() seeds them from the raw columns.
const insertPostingSQL = `
	WITH g AS (
		INSERT INTO tx_groups (user_id, timestamp, job_id)
		VALUES ($1, $4, $14)
		RETURNING id
	)
	INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 instrument_id, share_count_basis, account_type, group_id)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::date, $13, g.id FROM g
	RETURNING group_id
`

// insertPostingInGroupSQL inserts a tx as a posting of an existing tx group, for
// the second and subsequent legs of one economic event.
const insertPostingInGroupSQL = `
	INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 instrument_id, share_count_basis, account_type, group_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::date, $13, $14)
`

// groupResolver hands out the tx group for a tx's group_ref. An empty group_ref
// means the tx is its own single-posting group; a repeated one returns the group
// created for the first leg that used it. Refs are scoped to a single upload, so
// the resolver lives no longer than one call.
type groupResolver struct {
	byRef map[string]uuid.UUID
}

func newGroupResolver() *groupResolver {
	return &groupResolver{byRef: map[string]uuid.UUID{}}
}

// group returns the group id an already-created group should be reused for, and
// false when the caller must create one (an empty ref, or the first leg of a ref).
func (r *groupResolver) group(ref string) (uuid.UUID, bool) {
	if ref == "" {
		return uuid.UUID{}, false
	}
	id, ok := r.byRef[ref]
	return id, ok
}

// record remembers the group created for a non-empty ref so later legs join it.
func (r *groupResolver) record(ref string, id uuid.UUID) {
	if ref != "" {
		r.byRef[ref] = id
	}
}

// ReplaceTxsInPeriod implements db.TxDB.
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, userID, broker, jobID string, periodFrom, periodBefore *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string, shareCountBasis *time.Time) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	jobUUID, err := parseNullUUID(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	if len(instrumentIDs) != len(txs) {
		return fmt.Errorf("instrumentIDs length %d != txs length %d", len(instrumentIDs), len(txs))
	}
	return p.runInTx(ctx, func(exec queryable) error {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return fmt.Errorf("period_from: %w", err)
		}
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return fmt.Errorf("period_before: %w", err)
		}
		// Delete whole groups and let the FK cascade take their postings, so that a
		// replace can never leave half an economic event behind.
		// Synthetic txs (e.g. INITIALIZE rows backing holding declarations) are
		// managed by the declaration / recalc machinery, not by ingestion. Their
		// groups never match here, so a bulk replace cannot collaterally delete them.
		_, err = exec.ExecContext(ctx, `
			DELETE FROM tx_groups g
			WHERE g.user_id = $1
			  AND EXISTS (
			    SELECT 1 FROM txs t
			    WHERE t.group_id = g.id
			      AND t.broker = $2
			      AND t.timestamp >= $3 AND t.timestamp < $4
			      AND t.synthetic_purpose IS NULL
			  )
		`, userUUID, broker, fromT, beforeT)
		if err != nil {
			return fmt.Errorf("delete tx groups in period: %w", err)
		}
		// Postings written without a group (raw-SQL test fixtures) have no group to
		// cascade from and are cleared directly.
		_, err = exec.ExecContext(ctx, `
			DELETE FROM txs
			WHERE user_id = $1 AND broker = $2
			  AND timestamp >= $3 AND timestamp < $4
			  AND synthetic_purpose IS NULL
			  AND group_id IS NULL
		`, userUUID, broker, fromT, beforeT)
		if err != nil {
			return fmt.Errorf("delete ungrouped txs in period: %w", err)
		}
		resolver := newGroupResolver()
		for i, t := range txs {
			instUUID, err := uuid.Parse(instrumentIDs[i])
			if err != nil {
				return fmt.Errorf("invalid instrument id: %w", err)
			}
			ts, err := tsToTime(t.Timestamp)
			if err != nil {
				return err
			}
			txTypeStr, err := txTypeToStr(t.Type)
			if err != nil {
				return err
			}
			acctTypeStr, err := accountTypeToStr(t.GetAccountType())
			if err != nil {
				return err
			}
			acc := t.GetAccount()
			args := []interface{}{
				userUUID, broker, acc, ts, t.InstrumentDescription, txTypeStr, t.Quantity,
				nullStr(t.TradingCurrency), nullStr(t.SettlementCurrency), nullFloat(t.UnitPrice),
				instUUID, shareCountBasis, acctTypeStr,
			}
			ref := t.GetGroupRef()
			if groupID, ok := resolver.group(ref); ok {
				if _, err := exec.ExecContext(ctx, insertPostingInGroupSQL, append(args, groupID)...); err != nil {
					return fmt.Errorf("insert tx: %w", err)
				}
				continue
			}
			// The group takes the timestamp of the first leg that names it.
			var groupID uuid.UUID
			if err := exec.QueryRowContext(ctx, insertPostingSQL, append(args, jobUUID)...).Scan(&groupID); err != nil {
				return fmt.Errorf("insert tx: %w", err)
			}
			resolver.record(ref, groupID)
		}
		return nil
	})
}

// CreateTx implements db.TxDB. group_ref is ignored: a single-tx upload has nothing
// to group with.
func (p *Postgres) CreateTx(ctx context.Context, userID, broker, account, jobID string, tx *apiv1.Tx, instrumentID string, shareCountBasis *time.Time) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	jobUUID, err := parseNullUUID(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	ts, err := tsToTime(tx.Timestamp)
	if err != nil {
		return err
	}
	txTypeStr, err := txTypeToStr(tx.Type)
	if err != nil {
		return err
	}
	acctTypeStr, err := accountTypeToStr(tx.GetAccountType())
	if err != nil {
		return err
	}
	_, err = p.q.ExecContext(ctx, insertPostingSQL,
		userUUID, broker, account, ts, tx.InstrumentDescription, txTypeStr, tx.Quantity,
		nullStr(tx.TradingCurrency), nullStr(tx.SettlementCurrency), nullFloat(tx.UnitPrice),
		instUUID, shareCountBasis, acctTypeStr, jobUUID)
	if err != nil {
		return fmt.Errorf("create tx: %w", err)
	}
	return nil
}

// txOrderBy returns the ORDER BY clauses for a tx listing, with an id tiebreaker
// that makes the order total. Timestamps are not unique -- broker statements often
// supply only a date -- so without a tiebreaker the database is free to return
// tied rows in any order, and offset paging can then skip or repeat them across a
// page boundary.
func txOrderBy(prefix string, descending bool) []string {
	dir := ""
	if descending {
		dir = " DESC"
	}
	return []string{prefix + "timestamp" + dir, prefix + "id" + dir}
}

// ListTxs implements db.TxDB.
func (p *Postgres) ListTxs(ctx context.Context, userID string, broker *apiv1.Broker, account string, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("broker", "account", "timestamp", "instrument_description", "tx_type", "quantity", "split_adjusted_quantity", "trading_currency", "settlement_currency", "unit_price", "split_adjusted_unit_price", "instrument_id", "synthetic_purpose", "account_type").
		From("txs").
		Where(sq.Eq{"user_id": userUUID}).
		OrderBy(txOrderBy("", descending)...)
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, "", err
		}
		qb = qb.Where(sq.Eq{"broker": brokerStr})
	}
	if account != "" {
		qb = qb.Where(sq.Eq{"account": account})
	}
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		qb = qb.Where(sq.GtOrEq{"timestamp": fromT})
	}
	if periodBefore != nil {
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return nil, "", fmt.Errorf("period_before: %w", err)
		}
		qb = qb.Where(sq.Lt{"timestamp": beforeT})
	}
	offset := decodePageToken(pageToken)
	qb = qb.Limit(uint64(limit + 1)).Offset(uint64(offset))
	q, args, err := qb.ToSql()
	if err != nil {
		return nil, "", fmt.Errorf("build list txs query: %w", err)
	}
	var trows []txRow
	if err := p.q.SelectContext(ctx, &trows, q, args...); err != nil {
		return nil, "", fmt.Errorf("list txs: %w", err)
	}
	nextToken := ""
	if int32(len(trows)) > limit {
		trows = trows[:limit]
		nextToken = encodePageToken(offset + int64(limit))
	}
	out := make([]*apiv1.PortfolioTx, len(trows))
	for i := range trows {
		out[i] = trows[i].toProto()
	}
	return out, nextToken, nil
}

// ListTxsByPortfolio implements db.TxDB. Returns txs that match any of the portfolio's filters (OR), deduped.
func (p *Postgres) ListTxsByPortfolio(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("t.broker", "t.account", "t.timestamp", "t.instrument_description", "t.tx_type", "t.quantity", "t.split_adjusted_quantity", "t.trading_currency", "t.settlement_currency", "t.unit_price", "t.split_adjusted_unit_price", "t.instrument_id", "t.synthetic_purpose", "t.account_type").
		From("txs t").
		Join("portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = ?", portUUID).
		OrderBy(txOrderBy("t.", descending)...)
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, "", err
		}
		qb = qb.Where(sq.Eq{"t.broker": brokerStr})
	}
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		qb = qb.Where(sq.GtOrEq{"t.timestamp": fromT})
	}
	if periodBefore != nil {
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return nil, "", fmt.Errorf("period_before: %w", err)
		}
		qb = qb.Where(sq.Lt{"t.timestamp": beforeT})
	}
	offset := decodePageToken(pageToken)
	qb = qb.Limit(uint64(limit + 1)).Offset(uint64(offset))
	q, args, err := qb.ToSql()
	if err != nil {
		return nil, "", fmt.Errorf("build list txs by portfolio query: %w", err)
	}
	var trows []txRow
	if err := p.q.SelectContext(ctx, &trows, q, args...); err != nil {
		return nil, "", fmt.Errorf("list txs by portfolio: %w", err)
	}
	nextToken := ""
	if int32(len(trows)) > limit {
		trows = trows[:limit]
		nextToken = encodePageToken(offset + int64(limit))
	}
	out := make([]*apiv1.PortfolioTx, len(trows))
	for i := range trows {
		out[i] = trows[i].toProto()
	}
	return out, nextToken, nil
}
