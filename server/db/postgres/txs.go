package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
				INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			`, userUUID, broker, acc, ts, t.InstrumentDescription, txTypeStr, t.Quantity, nullStr(t.Currency), nullFloat(t.UnitPrice), instUUID)
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
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, userUUID, broker, account, ts, tx.InstrumentDescription, txTypeStr, tx.Quantity, nullStr(tx.Currency), nullFloat(tx.UnitPrice), instUUID)
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
	q := `
		SELECT broker, account, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id
		FROM txs WHERE user_id = $1
	`
	args := []interface{}{userUUID}
	argNum := 2
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, "", err
		}
		q += fmt.Sprintf(" AND broker = $%d", argNum)
		args = append(args, brokerStr)
		argNum++
	}
	if account != "" {
		q += fmt.Sprintf(" AND account = $%d", argNum)
		args = append(args, account)
		argNum++
	}
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		q += fmt.Sprintf(" AND timestamp >= $%d", argNum)
		args = append(args, fromT)
		argNum++
	}
	if periodTo != nil {
		toT, err := tsToTime(periodTo)
		if err != nil {
			return nil, "", fmt.Errorf("period_to: %w", err)
		}
		q += fmt.Sprintf(" AND timestamp <= $%d", argNum)
		args = append(args, toT)
		argNum++
	}
	q += " ORDER BY timestamp LIMIT $" + strconv.Itoa(argNum) + " OFFSET $" + strconv.Itoa(argNum+1)
	offset := int64(0)
	if pageToken != "" {
		b, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			offset, _ = strconv.ParseInt(string(b), 10, 64)
		}
	}
	args = append(args, limit+1, offset)
	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list txs: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.PortfolioTx
	var n int32
	for rows.Next() && n < limit {
		var brokerStr, accStr string
		var ts time.Time
		var instDesc, txTypeStr string
		var qty float64
		var currency sql.NullString
		var unitPrice sql.NullFloat64
		var instrumentID sql.NullString
		if err := rows.Scan(&brokerStr, &accStr, &ts, &instDesc, &txTypeStr, &qty, &currency, &unitPrice, &instrumentID); err != nil {
			return nil, "", err
		}
		tx := &apiv1.Tx{
			Timestamp:             timeToTs(ts),
			InstrumentDescription: instDesc,
			Type:                  strToTxType(txTypeStr),
			Quantity:              qty,
			Account:               accStr,
		}
		if currency.Valid {
			tx.Currency = currency.String
		}
		if unitPrice.Valid {
			tx.UnitPrice = unitPrice.Float64
		}
		if instrumentID.Valid {
			tx.InstrumentId = instrumentID.String
		}
		out = append(out, &apiv1.PortfolioTx{
			Broker:  strToBroker(brokerStr),
			Tx:      tx,
			Account: accStr,
		})
		n++
	}
	nextToken := ""
	if n == limit {
		nextToken = base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(offset+int64(limit), 10)))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
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
	q := `
		WITH matched AS (
			SELECT DISTINCT t.id
			FROM txs t
			INNER JOIN portfolio_filters f ON f.portfolio_id = $1::uuid
				AND (
					(f.filter_type = 'broker' AND t.broker = f.filter_value)
					OR (f.filter_type = 'account' AND t.account = f.filter_value)
					OR (f.filter_type = 'instrument' AND t.instrument_id IS NOT NULL AND t.instrument_id::text = f.filter_value)
				)
			WHERE t.user_id = (SELECT user_id FROM portfolios WHERE id = $2::uuid)
		)
		SELECT t.broker, t.account, t.timestamp, t.instrument_description, t.tx_type, t.quantity, t.currency, t.unit_price, t.instrument_id
		FROM txs t
		INNER JOIN matched m ON m.id = t.id
		WHERE 1=1
	`
	args := []interface{}{portUUID, portUUID}
	argNum := 3
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		q += fmt.Sprintf(" AND t.timestamp >= $%d", argNum)
		args = append(args, fromT)
		argNum++
	}
	if periodTo != nil {
		toT, err := tsToTime(periodTo)
		if err != nil {
			return nil, "", fmt.Errorf("period_to: %w", err)
		}
		q += fmt.Sprintf(" AND t.timestamp <= $%d", argNum)
		args = append(args, toT)
		argNum++
	}
	q += " ORDER BY t.timestamp LIMIT $" + strconv.Itoa(argNum) + " OFFSET $" + strconv.Itoa(argNum+1)
	offset := int64(0)
	if pageToken != "" {
		b, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			offset, _ = strconv.ParseInt(string(b), 10, 64)
		}
	}
	args = append(args, limit+1, offset)
	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list txs by portfolio: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.PortfolioTx
	var n int32
	for rows.Next() && n < limit {
		var brokerStr, accStr string
		var ts time.Time
		var instDesc, txTypeStr string
		var qty float64
		var currency sql.NullString
		var unitPrice sql.NullFloat64
		var instrumentID sql.NullString
		if err := rows.Scan(&brokerStr, &accStr, &ts, &instDesc, &txTypeStr, &qty, &currency, &unitPrice, &instrumentID); err != nil {
			return nil, "", err
		}
		tx := &apiv1.Tx{
			Timestamp:             timeToTs(ts),
			InstrumentDescription: instDesc,
			Type:                  strToTxType(txTypeStr),
			Quantity:              qty,
			Account:               accStr,
		}
		if currency.Valid {
			tx.Currency = currency.String
		}
		if unitPrice.Valid {
			tx.UnitPrice = unitPrice.Float64
		}
		if instrumentID.Valid {
			tx.InstrumentId = instrumentID.String
		}
		out = append(out, &apiv1.PortfolioTx{
			Broker:  strToBroker(brokerStr),
			Tx:      tx,
			Account: accStr,
		})
		n++
	}
	nextToken := ""
	if n == limit {
		nextToken = base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(offset+int64(limit), 10)))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return out, nextToken, nil
}
