package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ComputeHoldings implements db.HoldingsDB.
func (p *Postgres) ComputeHoldings(ctx context.Context, userID string, broker *apiv1.Broker, account string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid user id: %w", err)
	}
	asOfT := time.Now().UTC()
	if asOf != nil && asOf.IsValid() {
		asOfT = asOf.AsTime()
	}
	q := `
		SELECT broker, account, instrument_description, instrument_id,
			SUM(quantity) AS quantity
		FROM txs
		WHERE user_id = $1 AND timestamp <= $2
	`
	args := []interface{}{userUUID, asOfT}
	argNum := 3
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, nil, err
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
	q += `
		GROUP BY broker, account, instrument_description, instrument_id
		HAVING SUM(quantity) != 0
	`
	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Holding
	for rows.Next() {
		var brokerStr string
		var acct string
		var instDesc string
		var instrumentID sql.NullString
		var qty float64
		if err := rows.Scan(&brokerStr, &acct, &instDesc, &instrumentID, &qty); err != nil {
			return nil, nil, err
		}
		h := &apiv1.Holding{
			Broker:                strToBroker(brokerStr),
			InstrumentDescription: instDesc,
			Quantity:              qty,
			Account:               acct,
		}
		if instrumentID.Valid {
			h.InstrumentId = instrumentID.String
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
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
	rows, err := p.q.QueryContext(ctx, `
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
		SELECT t.broker, t.account, t.instrument_description, t.instrument_id, SUM(t.quantity) AS quantity
		FROM txs t
		INNER JOIN matched m ON m.id = t.id
		WHERE t.timestamp <= $3
		GROUP BY t.broker, t.account, t.instrument_description, t.instrument_id
		HAVING SUM(t.quantity) != 0
	`, portUUID, portUUID, asOfT)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings for portfolio: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Holding
	for rows.Next() {
		var brokerStr string
		var acct string
		var instDesc string
		var instrumentID sql.NullString
		var qty float64
		if err := rows.Scan(&brokerStr, &acct, &instDesc, &instrumentID, &qty); err != nil {
			return nil, nil, err
		}
		h := &apiv1.Holding{
			Broker:                strToBroker(brokerStr),
			InstrumentDescription: instDesc,
			Quantity:              qty,
			Account:               acct,
		}
		if instrumentID.Valid {
			h.InstrumentId = instrumentID.String
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, timeToTs(asOfT), nil
}
