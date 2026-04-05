package postgres

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)


// ReplaceTxsInPeriod implements db.TxDB.
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, userID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	if len(instrumentIDs) != len(txs) {
		return fmt.Errorf("instrumentIDs length %d != txs length %d", len(instrumentIDs), len(txs))
	}
	return p.runInTx(ctx, func(exec queryable) error {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return fmt.Errorf("period_from: %w", err)
		}
		toT, err := tsToTime(periodTo)
		if err != nil {
			return fmt.Errorf("period_to: %w", err)
		}
		_, err = exec.ExecContext(ctx, `
			DELETE FROM txs WHERE user_id = $1 AND broker = $2 AND timestamp >= $3 AND timestamp <= $4
		`, userUUID, broker, fromT, toT)
		if err != nil {
			return fmt.Errorf("delete txs in period: %w", err)
		}
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
			acc := t.GetAccount()
			_, err = exec.ExecContext(ctx, `
				INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, trading_currency, settlement_currency, unit_price, instrument_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			`, userUUID, broker, acc, ts, t.InstrumentDescription, txTypeStr, t.Quantity, nullStr(t.TradingCurrency), nullStr(t.SettlementCurrency), nullFloat(t.UnitPrice), instUUID)
			if err != nil {
				return fmt.Errorf("insert tx: %w", err)
			}
		}
		return nil
	})
}

// CreateTx implements db.TxDB.
func (p *Postgres) CreateTx(ctx context.Context, userID, broker, account string, tx *apiv1.Tx, instrumentID string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	ts, err := tsToTime(tx.Timestamp)
	if err != nil {
		return err
	}
	txTypeStr, err := txTypeToStr(tx.Type)
	if err != nil {
		return err
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, trading_currency, settlement_currency, unit_price, instrument_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, userUUID, broker, account, ts, tx.InstrumentDescription, txTypeStr, tx.Quantity, nullStr(tx.TradingCurrency), nullStr(tx.SettlementCurrency), nullFloat(tx.UnitPrice), instUUID)
	if err != nil {
		return fmt.Errorf("create tx: %w", err)
	}
	return nil
}

// ListTxs implements db.TxDB.
func (p *Postgres) ListTxs(ctx context.Context, userID string, broker *apiv1.Broker, account string, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("broker", "account", "timestamp", "instrument_description", "tx_type", "quantity", "trading_currency", "settlement_currency", "unit_price", "instrument_id", "synthetic_purpose").
		From("txs").
		Where(sq.Eq{"user_id": userUUID}).
		OrderBy("timestamp")
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
	if periodTo != nil {
		toT, err := tsToTime(periodTo)
		if err != nil {
			return nil, "", fmt.Errorf("period_to: %w", err)
		}
		qb = qb.Where(sq.LtOrEq{"timestamp": toT})
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
func (p *Postgres) ListTxsByPortfolio(ctx context.Context, portfolioID string, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("t.broker", "t.account", "t.timestamp", "t.instrument_description", "t.tx_type", "t.quantity", "t.trading_currency", "t.settlement_currency", "t.unit_price", "t.instrument_id", "t.synthetic_purpose").
		From("txs t").
		Join("portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = ?", portUUID).
		OrderBy("t.timestamp")
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		qb = qb.Where(sq.GtOrEq{"t.timestamp": fromT})
	}
	if periodTo != nil {
		toT, err := tsToTime(periodTo)
		if err != nil {
			return nil, "", fmt.Errorf("period_to: %w", err)
		}
		qb = qb.Where(sq.LtOrEq{"t.timestamp": toT})
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
