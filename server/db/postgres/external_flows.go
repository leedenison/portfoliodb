package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/db"
)

// externalFlowTail aggregates the external legs a scope's query selected into one
// row per day per commodity.
//
// The sign is inverted from the leg's own quantity, so positive is value entering.
// A group sums to zero per commodity, so the legs inside the scope sum to minus the
// legs outside it, and the outside ones are what the flow is. Check it against an
// INITIALIZE pad: USER +100 against EQUITY -100 gives +100, a contribution -- which
// is necessary, since a declared opening position with no flow behind it gives an
// infinite money-weighted return.
//
// The instrument is the commodity the value moved in, not a converted amount. A flow
// in shares would need a price as well as an FX rate to convert, which is the whole
// of the valuation query, and the result would be a double rather than the exact
// decimal a contribution figure should be. See
// docs/adr/0026-exact-decimals-bounded-by-closure.md.
//
// The HAVING suppresses a day whose external legs cancel -- most usefully an
// unmatched pair whose two sides landed on the same date, which moved no value in or
// out even though neither side is matched. The tolerance is built as valuation builds
// it, from the postings that could have rounded when they were converted into today's
// share count.
const externalFlowTail = `
	SELECT flow_date, instrument_id,
		CASE WHEN instrument_id IS NULL THEN instrument_description END AS instrument_description,
		SUM(amount) AS amount
	FROM external_legs
	GROUP BY flow_date, instrument_id,
		CASE WHEN instrument_id IS NULL THEN instrument_description END
	HAVING NOT qty_is_zero(SUM(amount), COUNT(*) FILTER (WHERE inexact)::int)
	ORDER BY flow_date, instrument_id NULLS LAST, 3`

// portfolioExternalFlowsSQL reads the flows crossing portfolio $1's cash-flow
// boundary over the half-open [$2, $3) window.
//
// A leg is external iff it is EQUITY, or a USER posting the portfolio does not
// match, or a TRANSFER_CLEARING posting portfolio_in_flight_txs does not admit. The
// remaining types -- INCOME, EXPENSE, IMBALANCE, SOURCE_ROUNDING -- are return and
// cost rather than contribution, and treating them as flows would strip dividends out
// of the return and report it gross of fees. Netting a matched transfer needs no case
// of its own: the pair is admitted by the same view valuation reads, so the two
// cannot disagree about what nets.
//
// touched_groups is the guard that keeps a non-member account's events out. Without
// it a dividend in an account the portfolio does not hold -- USER +100 against
// INCOME -100 -- has exactly one leg that is outside and external, and reports as a
// withdrawal from a portfolio the group never touched. It is deliberately not
// date-filtered: a group's legs need not share a date, and there is no bound on how
// far they can spread.
//
// Membership is per posting rather than per account, which an instrument filter makes
// visible: a buy is USER +10 shares, matched, and USER -1855 in cash, not matched, so
// the cash reads as a flow into the instrument-scoped sub-portfolio. That is the right
// answer for it -- the cash bought in -- and it agrees with valuation, which does not
// value that cash either.
//
// Both window bounds are applied here, where valuation applies only the upper one. A
// flow is an event and belongs to its date; a valuation is a state, and needs the
// history before the window to know the opening position.
//
// The date is the external leg's own. A group's postings need not share a timestamp,
// and the external leg is the counterparty of the flow, so its date is the one the
// source stated for the money crossing the boundary; choosing among several member
// legs instead would be arbitrary.
const portfolioExternalFlowsSQL = `
	WITH member_txs AS (
		SELECT m.tx_id FROM portfolio_matched_txs m WHERE m.portfolio_id = $1
	),
	touched_groups AS (
		SELECT DISTINCT t.group_id
		FROM txs t
		INNER JOIN member_txs mt ON mt.tx_id = t.id
		WHERE t.account_type = 'USER'
	),
	external_legs AS (
		SELECT t.order_date::date AS flow_date, t.instrument_id, t.instrument_description,
			-- The split-adjusted column, not the raw one: money never splits and the
			-- two agree for it, but the two sides of a securities journal recorded
			-- either side of a split are in different denominations.
			-t.split_adjusted_quantity AS amount,
			(t.split_adjusted_quantity <> t.quantity) AS inexact
		FROM txs t
		INNER JOIN touched_groups g ON g.group_id = t.group_id
		WHERE t.order_date::date >= $2 AND t.order_date::date < $3
			AND (t.account_type = 'EQUITY'
				OR (t.account_type = 'USER'
					AND NOT EXISTS (SELECT 1 FROM member_txs mt WHERE mt.tx_id = t.id))
				OR (t.account_type = 'TRANSFER_CLEARING'
					AND NOT EXISTS (SELECT 1 FROM portfolio_in_flight_txs f
						WHERE f.tx_id = t.id AND f.portfolio_id = $1)))
	)` + externalFlowTail

// userExternalFlowsSQL is the same question for a whole user, where every account is
// in scope.
//
// Two branches fall away rather than being suppressed. There is no USER leg outside
// the scope, so a transfer between two of the user's own accounts is internal
// whatever else is true of it, and with no outside USER leg there is nothing for a
// touched-groups guard to guard against either: a group with no member leg does not
// exist. What is left is EQUITY, which is value entering or leaving the user's
// holdings entirely, and an unmatched clearing leg, which is value that left for
// somewhere unknown.
const userExternalFlowsSQL = `
	WITH external_legs AS (
		SELECT t.order_date::date AS flow_date, t.instrument_id, t.instrument_description,
			-t.split_adjusted_quantity AS amount,
			(t.split_adjusted_quantity <> t.quantity) AS inexact
		FROM txs t
		WHERE t.user_id = $1
			AND t.order_date::date >= $2 AND t.order_date::date < $3
			AND (t.account_type = 'EQUITY'
				OR (t.account_type = 'TRANSFER_CLEARING'
					AND NOT EXISTS (SELECT 1 FROM transfer_matches tm
						WHERE tm.instrument_id = t.instrument_id
							AND (tm.from_group_id = t.group_id
								OR tm.to_group_id = t.group_id))))
	)` + externalFlowTail

// externalFlowRow is the sqlx-scannable shape for one day's flow in one commodity.
type externalFlowRow struct {
	Date        time.Time       `db:"flow_date"`
	InstID      *string         `db:"instrument_id"`
	Description *string         `db:"instrument_description"`
	Amount      decimal.Decimal `db:"amount"`
}

func (r *externalFlowRow) toDomain() db.ExternalFlow {
	f := db.ExternalFlow{Date: r.Date, Amount: r.Amount}
	if r.InstID != nil {
		f.InstrumentID = *r.InstID
	}
	if r.Description != nil {
		f.InstrumentDescription = *r.Description
	}
	return f
}

// GetPortfolioExternalFlows implements db.ExternalFlowDB.
func (p *Postgres) GetPortfolioExternalFlows(ctx context.Context, portfolioID string, dateFrom, dateBefore time.Time) ([]db.ExternalFlow, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	return p.queryExternalFlows(ctx, portfolioExternalFlowsSQL, portUUID, dateFrom, dateBefore)
}

// GetUserExternalFlows implements db.ExternalFlowDB.
func (p *Postgres) GetUserExternalFlows(ctx context.Context, userID string, dateFrom, dateBefore time.Time) ([]db.ExternalFlow, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	return p.queryExternalFlows(ctx, userExternalFlowsSQL, userUUID, dateFrom, dateBefore)
}

func (p *Postgres) queryExternalFlows(ctx context.Context, q string, scopeID uuid.UUID, dateFrom, dateBefore time.Time) ([]db.ExternalFlow, error) {
	var rows []externalFlowRow
	if err := p.q.SelectContext(ctx, &rows, q, scopeID, dateFrom, dateBefore); err != nil {
		return nil, fmt.Errorf("external flows query: %w", err)
	}
	out := make([]db.ExternalFlow, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}
