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
	qb := psql.Select("t.broker", "t.account",
		"COALESCE(i.name, MAX(t.instrument_description)) AS instrument_description",
		"t.instrument_id", "SUM(t.quantity) AS quantity").
		From("txs t").
		LeftJoin("instruments i ON i.id = t.instrument_id").
		Where(sq.Eq{"t.user_id": userUUID}).
		Where(sq.LtOrEq{"t.timestamp": asOfT}).
		GroupBy("t.broker", "t.account", "t.instrument_id", "i.name").
		Suffix("HAVING NOT qty_is_zero(SUM(t.quantity))")
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
			t.instrument_id, SUM(t.quantity) AS quantity
		FROM txs t
		INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
		LEFT JOIN instruments i ON i.id = t.instrument_id
		WHERE t.timestamp <= $2
		GROUP BY t.broker, t.account, t.instrument_id, i.name
		HAVING NOT qty_is_zero(SUM(t.quantity))
	`, portUUID, asOfT)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings for portfolio: %w", err)
	}
	out := make([]*apiv1.Holding, len(hrows))
	for i := range hrows {
		out[i] = hrows[i].toProto()
	}
	return out, timeToTs(asOfT), nil
}
