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

// declarationCols is the projection every declaration read shares, so a column added
// to the table reaches all of them at once.
const declarationCols = `id, user_id, broker, account, instrument_id, declared_qty, as_of_date, share_count_basis`

type declarationRow struct {
	ID              uuid.UUID `db:"id"`
	UserID          uuid.UUID `db:"user_id"`
	Broker          string    `db:"broker"`
	Account         string    `db:"account"`
	InstrumentID    uuid.UUID `db:"instrument_id"`
	DeclaredQty     string    `db:"declared_qty"`
	AsOfDate        time.Time `db:"as_of_date"`
	ShareCountBasis time.Time `db:"share_count_basis"`
}

func (r *declarationRow) toRow() *db.HoldingDeclarationRow {
	return &db.HoldingDeclarationRow{
		ID:              r.ID.String(),
		UserID:          r.UserID.String(),
		Broker:          r.Broker,
		Account:         r.Account,
		InstrumentID:    r.InstrumentID.String(),
		DeclaredQty:     r.DeclaredQty,
		AsOfDate:        r.AsOfDate,
		ShareCountBasis: r.ShareCountBasis,
	}
}

// CreateHoldingDeclaration implements db.HoldingDeclarationDB. A zero
// shareCountBasis leaves the column NULL so the table's trigger applies the
// as_of_date default, keeping the rule in one place.
func (p *Postgres) CreateHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time) (*db.HoldingDeclarationRow, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("invalid instrument id: %w", err)
	}
	var basis *time.Time
	if !shareCountBasis.IsZero() {
		basis = &shareCountBasis
	}
	var row declarationRow
	err = p.q.QueryRowxContext(ctx, `
		INSERT INTO holding_declarations (user_id, broker, account, instrument_id, declared_qty, as_of_date, share_count_basis)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+declarationCols,
		userUUID, broker, account, instUUID, declaredQty, asOfDate, basis).StructScan(&row)
	if err != nil {
		return nil, fmt.Errorf("create holding declaration: %w", err)
	}
	return row.toRow(), nil
}

// UpdateHoldingDeclaration implements db.HoldingDeclarationDB. The trigger that
// defaults share_count_basis fires on insert only, so a zero shareCountBasis here
// leaves the stored denomination alone rather than restating it from as_of_date.
func (p *Postgres) UpdateHoldingDeclaration(ctx context.Context, id, declaredQty string, asOfDate, shareCountBasis time.Time) (*db.HoldingDeclarationRow, error) {
	declID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid declaration id: %w", err)
	}
	var basis *time.Time
	if !shareCountBasis.IsZero() {
		basis = &shareCountBasis
	}
	var row declarationRow
	err = p.q.QueryRowxContext(ctx, `
		UPDATE holding_declarations
		SET declared_qty = $1, as_of_date = $2,
		    share_count_basis = COALESCE($3, share_count_basis),
		    updated_at = now()
		WHERE id = $4
		RETURNING `+declarationCols,
		declaredQty, asOfDate, basis, declID).StructScan(&row)
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
		SELECT `+declarationCols+`
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
		SELECT `+declarationCols+`
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

// ComputeRunningBalance implements db.HoldingDeclarationDB. Summing the raw
// quantities would mix share counts: a posting recorded before a split and one
// recorded after are in different units, and adding them scales the total by some
// part of the split factor. holding_qty_in_basis converts each posting into the
// declaration's basis first, so the pad reconciles a holding to a declaration
// denominated the same way. Synthetics are excluded -- a pad must not feed back
// into its own recomputation.
func (p *Postgres) ComputeRunningBalance(ctx context.Context, userID, broker, account, instrumentID string, from, to, basis time.Time) (decimal.Decimal, error) {
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
		SELECT qty FROM holding_qty_in_basis($1, $2, $3, $4, $5, $6, $7, false)
	`, userUUID, broker, account, instUUID, from, to, basis).Scan(&balance)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("compute running balance: %w", err)
	}
	return balance.Decimal, nil
}

// UpsertInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) UpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID string, init db.InitializeTx) error {
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
		var groupID uuid.UUID
		err := exec.QueryRowContext(ctx, `
			SELECT group_id FROM txs
			WHERE user_id = $1 AND broker = $2 AND account = $3 AND instrument_id = $4
			  AND synthetic_purpose = 'INITIALIZE'
			  AND account_type = 'USER'
		`, userUUID, broker, account, instUUID).Scan(&groupID)
		switch {
		case err == sql.ErrNoRows:
			if err := exec.QueryRowContext(ctx, `
				INSERT INTO tx_groups (user_id, timestamp) VALUES ($1, $2) RETURNING id
			`, userUUID, init.Timestamp).Scan(&groupID); err != nil {
				return fmt.Errorf("insert initialize tx group: %w", err)
			}
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
			{"USER", init.Quantity},
			{"EQUITY", init.Quantity.Neg()},
		} {
			// A pad has no price, so each leg weighs its own quantity in its own
			// commodity and the two cancel. The commodity is named the way
			// ownCommodity in server/service/ingestion/balance.go names it, so a
			// cash pad reads as the same commodity an ingested cash leg does.
			// weight is in the DO UPDATE list for the same reason quantity is:
			// otherwise a recalculation moves the quantity and leaves the weight
			// behind, and the group stops balancing.
			//
			// share_count_basis is written rather than left to the txs trigger, which
			// would seed it from the timestamp. The pad is dated at the portfolio
			// start date but denominated where its declaration is, and the two have no
			// reason to agree; letting the trigger decide also left the basis behind
			// whenever a recalculation moved the timestamp. It is in the DO UPDATE
			// list so both legs keep the same denomination as the declaration moves.
			if _, err := exec.ExecContext(ctx, `
				INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
				                 tx_type, quantity, instrument_id, synthetic_purpose,
				                 account_type, weight, weight_commodity, share_count_basis, group_id)
				SELECT $1, $2, $3, $4, 'INITIALIZE', $7, $5, $6, 'INITIALIZE', $8, $5,
				       CASE WHEN i.asset_class = 'CASH' AND i.currency IS NOT NULL
				            THEN 'cur:' || upper(i.currency)
				            ELSE 'inst:' || i.id::text END,
				       $9, $10
				FROM instruments i WHERE i.id = $6
				ON CONFLICT (user_id, broker, account, instrument_id, account_type)
				  WHERE synthetic_purpose = 'INITIALIZE'
				DO UPDATE SET timestamp = EXCLUDED.timestamp,
				              quantity = EXCLUDED.quantity,
				              tx_type = EXCLUDED.tx_type,
				              weight = EXCLUDED.weight,
				              weight_commodity = EXCLUDED.weight_commodity,
				              share_count_basis = EXCLUDED.share_count_basis
			`, userUUID, broker, account, init.Timestamp, leg.quantity, instUUID, init.TxType, leg.accountType, init.ShareCountBasis, groupID); err != nil {
				return fmt.Errorf("upsert initialize %s posting: %w", leg.accountType, err)
			}
		}
		if _, err := exec.ExecContext(ctx, `
			UPDATE tx_groups SET timestamp = $2 WHERE id = $1
		`, groupID, init.Timestamp); err != nil {
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
	// Delete the group and let the FK cascade take both legs of the pad, so neither
	// an empty group nor a lone counterparty is left behind.
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
		return nil
	})
}

// CreateDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) CreateDeclarationWithInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time, init db.InitializeTx) (*db.HoldingDeclarationRow, error) {
	var row *db.HoldingDeclarationRow
	err := p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		r, err := txp.CreateHoldingDeclaration(ctx, userID, broker, account, instrumentID, declaredQty, asOfDate, shareCountBasis)
		if err != nil {
			return err
		}
		row = r
		return txp.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, init)
	})
	return row, err
}

// UpdateDeclarationWithInitializeTx implements db.HoldingDeclarationDB.
func (p *Postgres) UpdateDeclarationWithInitializeTx(ctx context.Context, id, declaredQty string, asOfDate, shareCountBasis time.Time, userID, broker, account, instrumentID string, init db.InitializeTx) (*db.HoldingDeclarationRow, error) {
	var row *db.HoldingDeclarationRow
	err := p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		r, err := txp.UpdateHoldingDeclaration(ctx, id, declaredQty, asOfDate, shareCountBasis)
		if err != nil {
			return err
		}
		row = r
		return txp.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, init)
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
