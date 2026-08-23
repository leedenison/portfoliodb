package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

// declarationCols is the stored projection every declaration read shares, so a
// column added to the table reaches all of them at once. It is also what the write
// paths return: kind cannot be derived there, because an INSERT's own row is not
// visible to a subquery in its RETURNING clause, so a create would always call
// itself the pad.
const declarationCols = `id, user_id, broker, account, instrument_id, declared_qty, as_of_date, share_count_basis`

// declarationKind derives the pad/assert discriminator rather than storing it, so
// there is no column to keep in step with the rows. The earliest declaration for a
// holding seeds its opening balance; every later one is checked against the computed
// holding. Written as a correlated subquery rather than a window so that it is
// correct for a single-row read too, and the unique key on (user_id, broker,
// account, instrument_id, as_of_date) serves the lookup directly.
const declarationKind = `CASE WHEN d.as_of_date = (
	    SELECT MIN(e.as_of_date) FROM holding_declarations e
	    WHERE e.user_id = d.user_id AND e.broker = d.broker
	      AND e.account = d.account AND e.instrument_id = d.instrument_id)
	  THEN 'PAD' ELSE 'ASSERT' END AS kind`

// declarationVerify computes what the transactions actually add up to at a
// declaration's as_of_date, in the declaration's own share count. This is what turns
// a stored statement into a checked one.
//
// It is computed on read rather than stored. A holding's computed quantity moves
// under an ingestion, an instrument merge, an option split application and a split
// recompute alike, and there is no single place all of those pass through -- so a
// materialised verdict would need invalidating from each of them and would be stale
// the moment one was missed. Derived here it is current by construction, and it
// closes on its own when the data comes back into agreement.
//
// The pad is included (p_include_synthetic is true): an assertion checks the holding
// as the user sees it, opening balance and all. That is also why a pad's own
// assertion always reconciles -- the INITIALIZE tx is what makes it true.
const declarationVerify = `LATERAL holding_qty_in_basis(
	    d.user_id, d.broker, d.account, d.instrument_id,
	    NULL, (d.as_of_date + 1)::timestamptz, d.share_count_basis, true) v`

// declarationReadCols is the projection for reads, qualified for declarationKind and
// declarationVerify.
const declarationReadCols = `d.id, d.user_id, d.broker, d.account, d.instrument_id,
	d.declared_qty, d.as_of_date, d.share_count_basis,
	v.qty AS computed_qty, v.posting_count, v.inexact_bases, ` + declarationKind

type declarationRow struct {
	ID              uuid.UUID `db:"id"`
	UserID          uuid.UUID `db:"user_id"`
	Broker          string    `db:"broker"`
	Account         string    `db:"account"`
	InstrumentID    uuid.UUID `db:"instrument_id"`
	DeclaredQty     string    `db:"declared_qty"`
	AsOfDate        time.Time `db:"as_of_date"`
	ShareCountBasis time.Time `db:"share_count_basis"`
	// The verification columns and kind are absent from the write paths' projection.
	ComputedQty  *decimal.Decimal `db:"computed_qty"`
	PostingCount *int32           `db:"posting_count"`
	InexactBases *int32           `db:"inexact_bases"`
	Kind         *string          `db:"kind"`
}

func (r *declarationRow) toRow() *db.HoldingDeclarationRow {
	out := &db.HoldingDeclarationRow{
		ID:              r.ID.String(),
		UserID:          r.UserID.String(),
		Broker:          r.Broker,
		Account:         r.Account,
		InstrumentID:    r.InstrumentID.String(),
		DeclaredQty:     r.DeclaredQty,
		AsOfDate:        r.AsOfDate,
		ShareCountBasis: r.ShareCountBasis,
	}
	if r.Kind != nil {
		out.Kind = strToDeclarationKind(*r.Kind)
	}
	if r.ComputedQty != nil {
		out.Verified = &db.DeclarationCheck{
			ComputedQty:  *r.ComputedQty,
			PostingCount: derefInt32(r.PostingCount),
			InexactBases: derefInt32(r.InexactBases),
		}
	}
	return out
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func strToDeclarationKind(s string) apiv1.DeclarationKind {
	switch s {
	case "PAD":
		return apiv1.DeclarationKind_DECLARATION_KIND_PAD
	case "ASSERT":
		return apiv1.DeclarationKind_DECLARATION_KIND_ASSERT
	default:
		return apiv1.DeclarationKind_DECLARATION_KIND_UNSPECIFIED
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
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("create holding declaration: %w", db.ErrDuplicate)
		}
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
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("update holding declaration: %w", db.ErrDuplicate)
		}
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
		SELECT `+declarationReadCols+`
		FROM holding_declarations d, `+declarationVerify+`
		WHERE d.id = $1
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
		SELECT `+declarationReadCols+`
		FROM holding_declarations d, `+declarationVerify+`
		WHERE d.user_id = $1
		ORDER BY d.broker, d.account, d.instrument_id, d.as_of_date
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

// exportDeclaration is the scan target for ListHoldingDeclarationsForExport.
type exportDeclaration struct {
	Broker  string `db:"broker"`
	Account string `db:"account"`
	db.InstrumentRef
	DeclaredQty     decimal.Decimal `db:"declared_qty"`
	AsOfDate        time.Time       `db:"as_of_date"`
	ShareCountBasis *time.Time      `db:"share_count_basis"`
}

// ListHoldingDeclarationsForExport implements db.HoldingDeclarationDB.
//
// The identifier join is a LEFT JOIN so an instrument carrying no identifier
// still comes back, and the writer can say so rather than the row simply not
// appearing: a declaration silently missing from an export is the one failure
// mode a file the user diffs by hand cannot show them.
// bestIdentifierJoinOn rather than a hand-written lateral so this export agrees
// with every other one about which identifier is best.
//
// Neither declarationVerify nor declarationKind is here. The check and the
// pad/assert discriminator are derived from the declaration set and the
// postings, and are recomputed by whatever instance reads the file.
func (p *Postgres) ListHoldingDeclarationsForExport(ctx context.Context, userID string) ([]db.ExportDeclaration, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var rows []exportDeclaration
	err = p.q.SelectContext(ctx, &rows, `
		SELECT d.broker, d.account,
			COALESCE(best_id.identifier_type, '') AS identifier_type,
			COALESCE(best_id.value, '') AS value,
			COALESCE(best_id.domain, '') AS domain,
			d.declared_qty, d.as_of_date,
			-- A basis equal to the declaration's own date is the as-traded
			-- convention and says nothing a reader cannot infer. The column is
			-- NOT NULL and the insert trigger defaults it to that date, so
			-- selecting it raw would stamp a redundant date onto every row.
			CASE WHEN d.share_count_basis = d.as_of_date THEN NULL
				ELSE d.share_count_basis END AS share_count_basis
		FROM holding_declarations d
		`+bestIdentifierJoinOn("LEFT JOIN", "d.instrument_id", "best_id")+`
		WHERE d.user_id = $1
		ORDER BY d.broker, d.account, d.as_of_date, d.instrument_id
	`, userUUID)
	if err != nil {
		return nil, fmt.Errorf("list holding declarations for export: %w", err)
	}
	out := make([]db.ExportDeclaration, len(rows))
	for i, r := range rows {
		out[i] = db.ExportDeclaration{
			Broker:          r.Broker,
			Account:         r.Account,
			Ref:             r.InstrumentRef,
			DeclaredQty:     r.DeclaredQty,
			AsOfDate:        r.AsOfDate,
			ShareCountBasis: r.ShareCountBasis,
		}
	}
	return out, nil
}

// UpsertHoldingDeclaration implements db.HoldingDeclarationDB.
//
// The BEFORE INSERT trigger fills a NULL share_count_basis from as_of_date, and
// it runs before the conflict is detected, so EXCLUDED carries the defaulted
// value on the update branch too and the two branches agree on the denomination.
func (p *Postgres) UpsertHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	var basis *time.Time
	if !shareCountBasis.IsZero() {
		basis = &shareCountBasis
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO holding_declarations (user_id, broker, account, instrument_id, declared_qty, as_of_date, share_count_basis)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, broker, account, instrument_id, as_of_date)
		DO UPDATE SET declared_qty = EXCLUDED.declared_qty,
		              share_count_basis = EXCLUDED.share_count_basis,
		              updated_at = now()
	`, userUUID, broker, account, instUUID, declaredQty, asOfDate, basis)
	if err != nil {
		return fmt.Errorf("upsert holding declaration: %w", err)
	}
	return nil
}

// GetPortfolioStartDate implements db.HoldingDeclarationDB.
func (p *Postgres) GetPortfolioStartDate(ctx context.Context, userID string) (*time.Time, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var t sql.NullTime
	err = p.q.QueryRowContext(ctx, `
		SELECT MIN(order_date) FROM txs
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
			// The pad is one date twice: it is a synthetic opening balance rather
			// than a transaction anyone ordered, so it has no order-to-trade lag to
			// state and writing the start date to both columns says exactly that.
			//
			// share_count_basis is written rather than left to the txs trigger, which
			// would seed it from the trade date. The pad is dated at the portfolio
			// start date but denominated where its declaration is, and the two have no
			// reason to agree; letting the trigger decide also left the basis behind
			// whenever a recalculation moved the timestamp. It is in the DO UPDATE
			// list so both legs keep the same denomination as the declaration moves.
			// The type is the constant TRANSFER_EXTERNAL: a pad brings value in
			// from outside the user's holdings, which is that value's definition,
			// and its counter leg is already EQUITY. synthetic_purpose is what
			// marks it as a pad.
			if _, err := exec.ExecContext(ctx, `
				INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
				                 broker_tx_type, resolved_tx_type, quantity, instrument_id,
				                 listing_id,
				                 synthetic_purpose, account_type, weight, weight_commodity,
				                 share_count_basis, group_id)
				SELECT $1, $2, $3, $4, $4, 'INITIALIZE',
				       ARRAY['TRANSFER_EXTERNAL'], 'TRANSFER_EXTERNAL', $5, $6,
				       -- The pad offsets a holding, and a holding is per line, so it
				       -- has to be on the same one. The security's sole line where it
				       -- has exactly one, and none otherwise -- the same rungs the
				       -- ingest ladder runs, since a declaration states no currency of
				       -- its own. Naming the line on the declaration is 0149's next
				       -- step.
				       (SELECT l.id FROM instrument_listings l
				        WHERE l.instrument_id = i.id AND l.currency IS NOT NULL
				          AND NOT EXISTS (SELECT 1 FROM instrument_listings o
				                          WHERE o.instrument_id = i.id AND o.id <> l.id)),
				       'INITIALIZE', $7, $5,
				       CASE WHEN i.asset_class = 'CASH' AND i.currency IS NOT NULL
				            THEN 'cur:' || upper(i.currency)
				            ELSE 'inst:' || i.id::text END,
				       $8, $9
				FROM instruments i WHERE i.id = $6
				ON CONFLICT (user_id, broker, account, instrument_id, account_type)
				  WHERE synthetic_purpose = 'INITIALIZE'
				DO UPDATE SET order_date = EXCLUDED.order_date,
				              trade_date = EXCLUDED.trade_date,
				              quantity = EXCLUDED.quantity,
				              listing_id = EXCLUDED.listing_id,
				              weight = EXCLUDED.weight,
				              weight_commodity = EXCLUDED.weight_commodity,
				              share_count_basis = EXCLUDED.share_count_basis
			`, userUUID, broker, account, init.Timestamp, leg.quantity, instUUID, leg.accountType, init.ShareCountBasis, groupID); err != nil {
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
func (p *Postgres) DeleteDeclarationWithInitializeTx(ctx context.Context, id, userID, broker, account, instrumentID string, init *db.InitializeTx) error {
	return p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		if err := txp.DeleteHoldingDeclaration(ctx, id); err != nil {
			return err
		}
		// A holding keeps its pad for as long as it has any declaration left: what
		// was deleted may have been an assertion, or the pad with a later
		// declaration behind it ready to take over. Only an empty holding loses it.
		if init == nil {
			return txp.DeleteInitializeTx(ctx, userID, broker, account, instrumentID)
		}
		return txp.UpsertInitializeTx(ctx, userID, broker, account, instrumentID, *init)
	})
}
