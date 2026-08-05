package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
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
		WHERE user_id = $1 AND synthetic_purpose IS NULL AND account_type = 'USER'
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
func (p *Postgres) ComputeRunningBalance(ctx context.Context, userID, broker, account, instrumentID string, from, to time.Time) (decimal.Decimal, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("invalid instrument id: %w", err)
	}
	var balance decimal.NullDecimal
	err = p.q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0) FROM txs
		WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
		  AND timestamp >= $5 AND timestamp < $6
		  AND synthetic_purpose IS NULL
		  AND account_type = 'USER'
	`, userUUID, broker, account, instUUID, from, to).Scan(&balance)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("compute running balance: %w", err)
	}
	return balance.Decimal, nil
}

// UpsertInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) UpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID, txType string, timestamp time.Time, quantity decimal.Decimal) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	// The INITIALIZE postings and their group are updated in place rather than
	// re-inserted, so that recalculating a declaration does not orphan a group.
	// The group has no job: it is derived from the declaration, not ingested.
	//
	// A pad has no counterparty in the source data, so it is written with one: an
	// equal and opposite EQUITY posting of the same instrument in the same broker
	// account, which is what makes the group balance. Both legs are upserted on
	// the same key, so a recalculation moves them together and neither can drift
	// from the other. See docs/spec/postings.md and
	// docs/adr/0022-typed-per-account-cash-flow-boundary.md.
	return p.runInTx(ctx, func(exec queryable) error {
		var groupID uuid.NullUUID
		err := exec.QueryRowContext(ctx, `
			SELECT group_id FROM txs
			WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
			  AND synthetic_purpose = 'INITIALIZE'
			  AND account_type = 'USER'
		`, userUUID, broker, account, instUUID).Scan(&groupID)
		switch {
		case err == sql.ErrNoRows:
			var newGroupID uuid.UUID
			if err := exec.QueryRowContext(ctx, `
				INSERT INTO tx_groups (user_id, timestamp) VALUES ($1, $2) RETURNING id
			`, userUUID, timestamp).Scan(&newGroupID); err != nil {
				return fmt.Errorf("insert initialize tx group: %w", err)
			}
			groupID = uuid.NullUUID{UUID: newGroupID, Valid: true}
		case err != nil:
			return fmt.Errorf("find initialize tx: %w", err)
		}
		// Upsert rather than update: the EQUITY leg is absent from any pad written
		// before it existed, and inserting it there is the same operation as
		// keeping it in step afterwards. The conflict target is the partial unique
		// index on INITIALIZE postings, which has account_type in its key so the
		// two legs do not collide.
		for _, leg := range []struct {
			accountType string
			quantity    decimal.Decimal
		}{
			{"USER", quantity},
			{"EQUITY", quantity.Neg()},
		} {
			if _, err := exec.ExecContext(ctx, `
				INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
				                 tx_type, quantity, instrument_id, synthetic_purpose,
				                 account_type, group_id)
				VALUES ($1, $2, $3, $4, 'INITIALIZE', $7, $5, $6, 'INITIALIZE', $8, $9)
				ON CONFLICT (user_id, broker, account, instrument_id, account_type)
				  WHERE synthetic_purpose = 'INITIALIZE'
				DO UPDATE SET timestamp = EXCLUDED.timestamp,
				              quantity = EXCLUDED.quantity,
				              tx_type = EXCLUDED.tx_type
			`, userUUID, broker, account, timestamp, leg.quantity, instUUID, txType, leg.accountType, groupID); err != nil {
				return fmt.Errorf("upsert initialize %s posting: %w", leg.accountType, err)
			}
		}
		if !groupID.Valid {
			return nil
		}
		if _, err := exec.ExecContext(ctx, `
			UPDATE tx_groups SET timestamp = $2 WHERE id = $1
		`, groupID.UUID, timestamp); err != nil {
			return fmt.Errorf("update initialize tx group: %w", err)
		}
		return nil
	})
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
	// Delete the group and let the FK cascade take the posting, so no empty group
	// is left behind. Postings written without a group are cleared directly.
	return p.runInTx(ctx, func(exec queryable) error {
		_, err := exec.ExecContext(ctx, `
			DELETE FROM tx_groups g
			WHERE g.user_id = $1
			  AND EXISTS (
			    SELECT 1 FROM txs t
			    WHERE t.group_id = g.id
			      AND t.broker = $2 AND t.account = $3 AND t.instrument_id = $4
			      AND t.synthetic_purpose = 'INITIALIZE'
			  )
		`, userUUID, broker, account, instUUID)
		if err != nil {
			return fmt.Errorf("delete initialize tx group: %w", err)
		}
		_, err = exec.ExecContext(ctx, `
			DELETE FROM txs
			WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
			  AND synthetic_purpose = 'INITIALIZE'
			  AND group_id IS NULL
		`, userUUID, broker, account, instUUID)
		if err != nil {
			return fmt.Errorf("delete initialize tx: %w", err)
		}
		return nil
	})
}

// CreateDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) CreateDeclarationWithInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate time.Time, initTxType string, initTimestamp time.Time, initQty decimal.Decimal) (*db.HoldingDeclarationRow, error) {
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
func (p *Postgres) UpdateDeclarationWithInitializeTx(ctx context.Context, id, declaredQty string, asOfDate time.Time, userID, broker, account, instrumentID, initTxType string, initTimestamp time.Time, initQty decimal.Decimal) (*db.HoldingDeclarationRow, error) {
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
