package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
)

type declarationRow struct {
	ID           uuid.UUID `db:"id"`
	UserID       uuid.UUID `db:"user_id"`
	Broker       string    `db:"broker"`
	Account      string    `db:"account"`
	InstrumentID uuid.UUID `db:"instrument_id"`
	DeclaredQty  string    `db:"declared_qty"`
	AsOfDate     time.Time `db:"as_of_date"`
}

func (r *declarationRow) toRow() *db.HoldingDeclarationRow {
	return &db.HoldingDeclarationRow{
		ID:           r.ID.String(),
		UserID:       r.UserID.String(),
		Broker:       r.Broker,
		Account:      r.Account,
		InstrumentID: r.InstrumentID.String(),
		DeclaredQty:  r.DeclaredQty,
		AsOfDate:     r.AsOfDate,
	}
}

// CreateHoldingDeclaration implements db.HoldingDeclarationDB.
func (p *Postgres) CreateHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate time.Time) (*db.HoldingDeclarationRow, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("invalid instrument id: %w", err)
	}
	var row declarationRow
	err = p.q.QueryRowxContext(ctx, `
		INSERT INTO holding_declarations (user_id, broker, account, instrument_id, declared_qty, as_of_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, broker, account, instrument_id, declared_qty, as_of_date
	`, userUUID, broker, account, instUUID, declaredQty, asOfDate).StructScan(&row)
	if err != nil {
		return nil, fmt.Errorf("create holding declaration: %w", err)
	}
	return row.toRow(), nil
}

// UpdateHoldingDeclaration implements db.HoldingDeclarationDB.
func (p *Postgres) UpdateHoldingDeclaration(ctx context.Context, id, declaredQty string, asOfDate time.Time) (*db.HoldingDeclarationRow, error) {
	declID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid declaration id: %w", err)
	}
	var row declarationRow
	err = p.q.QueryRowxContext(ctx, `
		UPDATE holding_declarations
		SET declared_qty = $1, as_of_date = $2, updated_at = now()
		WHERE id = $3
		RETURNING id, user_id, broker, account, instrument_id, declared_qty, as_of_date
	`, declaredQty, asOfDate, declID).StructScan(&row)
	if err != nil {
		return nil, fmt.Errorf("update holding declaration: %w", err)
	}
	return row.toRow(), nil
}

// DeleteHoldingDeclaration implements db.HoldingDeclarationDB.
func (p *Postgres) DeleteHoldingDeclaration(ctx context.Context, id string) error {
	declID, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid declaration id: %w", err)
	}
	res, err := p.q.ExecContext(ctx, `DELETE FROM holding_declarations WHERE id = $1`, declID)
	if err != nil {
		return fmt.Errorf("delete holding declaration: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetHoldingDeclaration implements db.HoldingDeclarationDB.
func (p *Postgres) GetHoldingDeclaration(ctx context.Context, id string) (*db.HoldingDeclarationRow, error) {
	declID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid declaration id: %w", err)
	}
	var row declarationRow
	err = p.q.QueryRowxContext(ctx, `
		SELECT id, user_id, broker, account, instrument_id, declared_qty, as_of_date
		FROM holding_declarations WHERE id = $1
	`, declID).StructScan(&row)
	if err != nil {
		return nil, fmt.Errorf("get holding declaration: %w", err)
	}
	return row.toRow(), nil
}

// ListHoldingDeclarations implements db.HoldingDeclarationDB.
func (p *Postgres) ListHoldingDeclarations(ctx context.Context, userID string) ([]*db.HoldingDeclarationRow, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var rows []declarationRow
	err = p.q.SelectContext(ctx, &rows, `
		SELECT id, user_id, broker, account, instrument_id, declared_qty, as_of_date
		FROM holding_declarations WHERE user_id = $1
		ORDER BY broker, account, as_of_date
	`, userUUID)
	if err != nil {
		return nil, fmt.Errorf("list holding declarations: %w", err)
	}
	out := make([]*db.HoldingDeclarationRow, len(rows))
	for i := range rows {
		out[i] = rows[i].toRow()
	}
	return out, nil
}

// GetPortfolioStartDate implements db.HoldingDeclarationDB.
func (p *Postgres) GetPortfolioStartDate(ctx context.Context, userID string) (*time.Time, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var t sql.NullTime
	err = p.q.QueryRowContext(ctx, `
		SELECT MIN(timestamp) FROM txs
		WHERE user_id = $1 AND synthetic_purpose IS NULL
	`, userUUID).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("get portfolio start date: %w", err)
	}
	if !t.Valid {
		return nil, nil
	}
	return &t.Time, nil
}

// ComputeRunningBalance implements db.HoldingDeclarationDB.
func (p *Postgres) ComputeRunningBalance(ctx context.Context, userID, broker, account, instrumentID string, from, to time.Time) (float64, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return 0, fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return 0, fmt.Errorf("invalid instrument id: %w", err)
	}
	var balance sql.NullFloat64
	err = p.q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0) FROM txs
		WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
		  AND timestamp >= $5 AND timestamp < $6
		  AND synthetic_purpose IS NULL
	`, userUUID, broker, account, instUUID, from, to).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("compute running balance: %w", err)
	}
	return balance.Float64, nil
}

// UpsertInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) UpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID, txType string, timestamp time.Time, quantity float64) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, instrument_id, synthetic_purpose)
		VALUES ($1, $2, $3, $4, 'INITIALIZE', $7, $5, $6, 'INITIALIZE')
		ON CONFLICT (user_id, broker, account, instrument_id) WHERE synthetic_purpose = 'INITIALIZE'
		DO UPDATE SET timestamp = EXCLUDED.timestamp, quantity = EXCLUDED.quantity, tx_type = EXCLUDED.tx_type
	`, userUUID, broker, account, timestamp, quantity, instUUID, txType)
	if err != nil {
		return fmt.Errorf("upsert initialize tx: %w", err)
	}
	return nil
}

// DeleteInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) DeleteInitializeTx(ctx context.Context, userID, broker, account, instrumentID string) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `
		DELETE FROM txs
		WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
		  AND synthetic_purpose = 'INITIALIZE'
	`, userUUID, broker, account, instUUID)
	if err != nil {
		return fmt.Errorf("delete initialize tx: %w", err)
	}
	return nil
}

// CreateDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) CreateDeclarationWithInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate time.Time, initTxType string, initTimestamp time.Time, initQty float64) (*db.HoldingDeclarationRow, error) {
	var row *db.HoldingDeclarationRow
	err := p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		r, err := txp.CreateHoldingDeclaration(ctx, userID, broker, account, instrumentID, declaredQty, asOfDate)
		if err != nil {
			return err
		}
		row = r
		return txp.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, initTxType, initTimestamp, initQty)
	})
	return row, err
}

// UpdateDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) UpdateDeclarationWithInitializeTx(ctx context.Context, id, declaredQty string, asOfDate time.Time, userID, broker, account, instrumentID, initTxType string, initTimestamp time.Time, initQty float64) (*db.HoldingDeclarationRow, error) {
	var row *db.HoldingDeclarationRow
	err := p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		r, err := txp.UpdateHoldingDeclaration(ctx, id, declaredQty, asOfDate)
		if err != nil {
			return err
		}
		row = r
		return txp.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, initTxType, initTimestamp, initQty)
	})
	return row, err
}

// DeleteDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) DeleteDeclarationWithInitializeTx(ctx context.Context, id, userID, broker, account, instrumentID string) error {
	return p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		if err := txp.DeleteHoldingDeclaration(ctx, id); err != nil {
			return err
		}
		return txp.DeleteInitializeTx(ctx, userID, broker, account, instrumentID)
	})
}
