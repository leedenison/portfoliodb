package postgres

import (
	"context"
	"database/sql"
	"fmt"
	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/residual"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// insertPostingSQL inserts a tx as the single posting of a new tx group. The group
// is created in the same statement so that grouping costs no extra round trip.
// split_adjusted_quantity / split_adjusted_unit_price are omitted deliberately:
// default_split_adjusted_tx() seeds them from the raw columns.
const insertPostingSQL = `
	WITH g AS (
		INSERT INTO tx_groups (user_id, timestamp, job_id)
		VALUES ($1, $4, $19)
		RETURNING id
	)
	INSERT INTO txs (user_id, broker, account, order_date, instrument_description,
	                 broker_tx_type, resolved_tx_type, asset_class_hint,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 settlement_amount, instrument_id, account_type,
	                 synthetic_purpose, weight, weight_commodity, group_id, trade_date)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, g.id, $20 FROM g
	RETURNING id, group_id
`

// insertPostingInGroupSQL inserts a tx as a posting of an existing tx group, for
// the second and subsequent legs of one economic event.
const insertPostingInGroupSQL = `
	INSERT INTO txs (user_id, broker, account, order_date, instrument_description,
	                 broker_tx_type, resolved_tx_type, asset_class_hint,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 settlement_amount, instrument_id, account_type,
	                 synthetic_purpose, weight, weight_commodity, group_id, trade_date)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	RETURNING id
`

// insertCorrelationSQL records one of a posting's correlations. ordinality is the
// position the source stated it in, so a posting read back is the posting that
// was written.
const insertCorrelationSQL = `
	INSERT INTO tx_correlations (tx_id, ordinality, label, token, ordinal, scope, matches,
	                             ordinal_span, job_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`

// written is what an insert put down: the postings, for the settler to start from,
// and the groups they landed in, for the caller to settle.
type written struct {
	postings []uuid.UUID
	groups   []uuid.UUID
}

// ReplaceTxsInPeriod implements db.TxDB.
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, userID, broker, jobID string, periodFrom, periodBefore *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight) error {
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
		var touched settleSet
		cut, err := clearPeriod(ctx, exec, userUUID, broker, fromT, beforeT)
		if err != nil {
			return err
		}
		// A group the replace cut has lost legs, so what it fails to balance to is
		// their value and can be any size.
		touched.addAll(cut, true)
		written, err := insertPostings(ctx, exec, userUUID, broker, jobUUID, txs, instrumentIDs, weights, "")
		if err != nil {
			return err
		}
		touched.addAll(written.groups, false)
		if err := p.groupWritten(ctx, exec, userUUID, written.postings, &touched); err != nil {
			return err
		}
		return touched.settle(ctx, exec, userUUID)
	})
}

// groupWritten hands the postings just written to the settler, and applies whatever
// partition it decides.
//
// The seed is what this write put down, and the engine grows the region from there,
// so legs that landed beside ones already stored are joined here rather than waiting
// for the cadence to notice. It reads the transaction in progress, which is what lets
// it see both.
//
// A store with no settler leaves each posting where insertPostings put it. That is
// what the fixtures that are not about grouping want, and it is the only behaviour
// available to a caller that has not wired the engine in.
func (p *Postgres) groupWritten(ctx context.Context, exec queryable, userUUID uuid.UUID, postingIDs []uuid.UUID, touched *settleSet) error {
	if p.settler == nil || len(postingIDs) == 0 {
		return nil
	}
	seed, err := groupingPostingsByID(ctx, exec, userUUID, postingIDs)
	if err != nil {
		return err
	}
	if len(seed) == 0 {
		return nil
	}
	changes, err := p.settler.Settle(ctx, userUUID.String(), seed, NewWithQueryable(exec))
	if err != nil {
		return fmt.Errorf("derive groups: %w", err)
	}
	if len(changes) == 0 {
		return nil
	}
	_, err = applyChanges(ctx, exec, userUUID, changes, touched)
	return err
}

// insertPostings writes each tx as a posting of a group of its own. account
// overrides the account on every posting when non-empty, for the append path where
// the whole group belongs to one named account.
//
// One group per posting, because nothing the wire carries says which postings are
// legs of one event any more: the settler decides that, from the evidence the
// postings carry, once they are stored. A posting alone in a group is the shape a
// partition is derived over rather than a claim that it is a whole event.
//
// weights is parallel to txs, or nil when the caller has
// none. See db.TxDB for what a missing entry in either means.
func insertPostings(ctx context.Context, exec queryable, userUUID uuid.UUID, broker string, jobUUID interface{}, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight, account string) (written, error) {
	var out written
	for i, t := range txs {
		instUUID, err := uuid.Parse(instrumentIDs[i])
		if err != nil {
			return out, fmt.Errorf("invalid instrument id: %w", err)
		}
		ordered, err := tsToTime(t.OrderDate)
		if err != nil {
			return out, err
		}
		traded, err := tsToTime(t.TradeDate)
		if err != nil {
			return out, err
		}
		brokerTypes, err := db.TxTypesToStrs(t.GetBrokerTxType())
		if err != nil {
			return out, err
		}
		// Erroring on an unresolved value is what stops any path storing a
		// posting the ingest pipeline did not resolve.
		resolvedStr, err := txTypeToStr(t.GetResolvedTxType())
		if err != nil {
			return out, err
		}
		acctTypeStr, err := accountTypeToStr(t.GetAccountType())
		if err != nil {
			return out, err
		}
		acc := t.GetAccount()
		if account != "" {
			acc = account
		}
		// The wire carries decimals as strings, so this is where a posting's
		// quantity and price become values. A malformed one is the caller's
		// fault: the protovalidate patterns reject it at the interceptor for
		// every unary RPC, so reaching this error means an internal caller
		// built the posting badly.
		qty, err := decimal.NewFromString(t.GetQuantity())
		if err != nil {
			return out, fmt.Errorf("invalid quantity %q: %w", t.GetQuantity(), err)
		}
		price, err := parseOptDecimal(t.UnitPrice)
		if err != nil {
			return out, fmt.Errorf("invalid unit price %q: %w", t.GetUnitPrice(), err)
		}
		settlement, err := parseOptDecimal(t.SettlementAmount)
		if err != nil {
			return out, fmt.Errorf("invalid settlement amount %q: %w", t.GetSettlementAmount(), err)
		}
		w := db.Weight{Amount: qty, Commodity: "inst:" + instUUID.String()}
		if i < len(weights) {
			w = weights[i]
		}
		args := []interface{}{
			userUUID, broker, acc, ordered, t.InstrumentDescription,
			pq.Array(brokerTypes), resolvedStr, nullStr(db.AssetClassToStr(t.GetAssetClassHint())), qty,
			nullStr(t.TradingCurrency), nullStr(t.SettlementCurrency), nullDecimal(price),
			nullDecimal(settlement), instUUID, acctTypeStr,
			nullStr(t.GetSyntheticPurpose()), w.Amount, w.Commodity,
		}
		var txID, groupID uuid.UUID
		if err := exec.QueryRowContext(ctx, insertPostingSQL, append(args, jobUUID, traded)...).Scan(&txID, &groupID); err != nil {
			return out, fmt.Errorf("insert tx: %w", err)
		}
		out.postings = append(out.postings, txID)
		out.groups = append(out.groups, groupID)
		if err := insertCorrelations(ctx, exec, txID, jobUUID, t.GetCorrelations()); err != nil {
			return out, err
		}
	}
	return out, nil
}

// insertCorrelations records what a posting's source said about why it might
// belong with another one. The job is stored beside each one because it is what a
// FILE-scoped correlation is comparable within.
func insertCorrelations(ctx context.Context, exec queryable, txID uuid.UUID, jobUUID interface{}, cs []*archivev1.Correlation) error {
	for i, c := range cs {
		scope, err := db.ScopeToStr(c.GetScope())
		if err != nil {
			return fmt.Errorf("correlation %q: %w", c.GetToken(), err)
		}
		matches := make(pq.StringArray, 0, len(c.GetMatch()))
		for _, m := range c.GetMatch() {
			s, err := db.MatchToStr(m)
			if err != nil {
				return fmt.Errorf("correlation %q: %w", c.GetToken(), err)
			}
			matches = append(matches, s)
		}
		if len(matches) == 0 {
			return fmt.Errorf("correlation %q: no match declared", c.GetToken())
		}
		if _, err := exec.ExecContext(ctx, insertCorrelationSQL,
			txID, i, c.GetLabel(), c.GetToken(), c.Ordinal, scope, matches, c.OrdinalSpan, jobUUID,
		); err != nil {
			return fmt.Errorf("insert tx correlation: %w", err)
		}
	}
	return nil
}

// CreateTxGroup implements db.TxDB. It appends postings without replacing a period,
// which is the manual-entry path: CreateTx wraps its one posting in a window with no
// period, and that is where this diverges from ReplaceTxsInPeriod. The postings share
// the named account.
//
// It no longer forces them into one group. Nothing on the wire says which postings
// are legs of one event, so each is stored alone and the settler decides -- for a
// manually added posting exactly as for an uploaded one, which is the point: a
// posting a person entered is evidence like any other and gets no privileged
// partition. What it keeps from the old behaviour is the balance: whatever it does
// not account for is routed to a counterparty, so a manual entry that balances
// against nothing is still a group the invariant can reach.
func (p *Postgres) CreateTxGroup(ctx context.Context, userID, broker, account, jobID string, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	jobUUID, err := parseNullUUID(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	return p.runInTx(ctx, func(exec queryable) error {
		var touched settleSet
		w, err := insertPostings(ctx, exec, userUUID, broker, jobUUID, txs, instrumentIDs, weights, account)
		if err != nil {
			return fmt.Errorf("create tx group: %w", err)
		}
		touched.addAll(w.groups, false)
		if err := p.groupWritten(ctx, exec, userUUID, w.postings, &touched); err != nil {
			return err
		}
		return touched.settle(ctx, exec, userUUID)
	})
}

// txOrderBy returns the ORDER BY clauses for a tx listing, with an id tiebreaker
// that makes the order total. Dates are not unique -- broker statements often
// supply only a date -- so without a tiebreaker the database is free to return
// tied rows in any order, and offset paging can then skip or repeat them across a
// page boundary.
//
// The date column is named rather than derived from the prefix because the two
// tables spell it differently: a group carries one timestamp, while a posting
// carries an order date and a trade date and a listing orders by the first.
func txOrderBy(dateCol, idCol string, descending bool) []string {
	dir := ""
	if descending {
		dir = " DESC"
	}
	return []string{dateCol + dir, idCol + dir}
}

// txListCols are the posting columns a listing returns, which is what txRow scans.
var txListCols = []string{
	"t.broker", "t.account", "t.order_date", "t.instrument_description",
	"t.broker_tx_type", "t.resolved_tx_type", "t.asset_class_hint", "t.quantity",
	"t.split_adjusted_quantity", "t.trading_currency", "t.settlement_currency",
	"t.unit_price", "t.split_adjusted_unit_price", "t.instrument_id",
	"t.synthetic_purpose", "t.account_type", "t.group_id::text AS group_id",
	"g.timestamp AS group_timestamp",
}

// txListBase is the shape both listings share: postings, aliased t, joined to the
// group each is a leg of and filtered by broker and period. The columns are left to
// the caller, because a page is read in two passes -- which groups it covers, then
// their postings -- and the two ask different columns of the same filtered set.
func txListBase(broker *typev1.Broker, periodFrom, periodBefore *timestamppb.Timestamp) (sq.SelectBuilder, error) {
	qb := psql.Select().From("txs t").Join("tx_groups g ON g.id = t.group_id")
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return qb, err
		}
		qb = qb.Where(sq.Eq{"t.broker": brokerStr})
	}
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return qb, fmt.Errorf("period_from: %w", err)
		}
		qb = qb.Where(sq.GtOrEq{"t.order_date": fromT})
	}
	if periodBefore != nil {
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return qb, fmt.Errorf("period_before: %w", err)
		}
		qb = qb.Where(sq.Lt{"t.order_date": beforeT})
	}
	return qb, nil
}

// groupPageRow is the sqlx-scannable shape of the group-page query. The timestamp
// is selected only so that DISTINCT and ORDER BY agree about it.
type groupPageRow struct {
	ID        uuid.UUID `db:"id"`
	Timestamp time.Time `db:"timestamp"`
}

// listTxPage reads one page of a tx listing: every posting of the groups the page
// covers, ordered by event and then within the event.
//
// A page is a whole number of groups rather than of postings, because a group that
// straddled a page boundary would reach the client as two partial events, neither
// carrying the legs it takes to tell which one is the principal. pageSize therefore
// counts groups, and how many rows a page holds is however many legs those groups
// have.
//
// The filters still select postings, not groups: a group is on the page when at
// least one of its postings passes them, and only the postings that passed are
// returned. So a group straddling a period bound contributes its in-period legs,
// which is what ListTxsForExport does with the same bounds, and a portfolio view
// shows the legs its filters matched rather than the whole event.
func listTxPage(ctx context.Context, q queryable, base sq.SelectBuilder, descending bool, limit int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	offset := decodePageToken(pageToken)
	groupOrder := txOrderBy("g.timestamp", "g.id", descending)
	gsql, gargs, err := base.Columns("g.id", "g.timestamp").Distinct().
		OrderBy(groupOrder...).
		Limit(uint64(limit + 1)).Offset(uint64(offset)).ToSql()
	if err != nil {
		return nil, "", fmt.Errorf("build group page query: %w", err)
	}
	var grows []groupPageRow
	if err := q.SelectContext(ctx, &grows, gsql, gargs...); err != nil {
		return nil, "", fmt.Errorf("list tx groups: %w", err)
	}
	nextToken := ""
	if int32(len(grows)) > limit {
		grows = grows[:limit]
		nextToken = encodePageToken(offset + int64(limit))
	}
	if len(grows) == 0 {
		return nil, "", nil
	}
	ids := make([]uuid.UUID, len(grows))
	for i := range grows {
		ids[i] = grows[i].ID
	}
	// Within a group the postings follow the same direction as the groups, so the
	// first row of a descending listing is still the most recent posting -- which is
	// what asking for one page of one group descending means (adr/0015).
	tsql, pargs, err := base.Columns(txListCols...).
		Where(sq.Eq{"t.group_id": ids}).
		OrderBy(append(groupOrder, txOrderBy("t.order_date", "t.id", descending)...)...).ToSql()
	if err != nil {
		return nil, "", fmt.Errorf("build tx page query: %w", err)
	}
	var trows []txRow
	if err := q.SelectContext(ctx, &trows, tsql, pargs...); err != nil {
		return nil, "", fmt.Errorf("list txs: %w", err)
	}
	out := make([]*apiv1.PortfolioTx, len(trows))
	for i := range trows {
		out[i] = trows[i].toProto()
	}
	return out, nextToken, nil
}

// ListTxs implements db.TxDB.
func (p *Postgres) ListTxs(ctx context.Context, userID string, broker *typev1.Broker, account string, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	qb, err := txListBase(broker, periodFrom, periodBefore)
	if err != nil {
		return nil, "", err
	}
	qb = qb.Where(sq.Eq{"t.user_id": userUUID})
	if account != "" {
		qb = qb.Where(sq.Eq{"t.account": account})
	}
	return listTxPage(ctx, p.q, qb, descending, txPageLimit(pageSize), pageToken)
}

// ListTxsByPortfolio implements db.TxDB. Returns txs that match any of the portfolio's filters (OR), deduped.
func (p *Postgres) ListTxsByPortfolio(ctx context.Context, portfolioID string, broker *typev1.Broker, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	qb, err := txListBase(broker, periodFrom, periodBefore)
	if err != nil {
		return nil, "", err
	}
	qb = qb.Join("portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = ?", portUUID)
	return listTxPage(ctx, p.q, qb, descending, txPageLimit(pageSize), pageToken)
}

// txPageLimit clamps a requested page size to the range a listing serves. It counts
// groups; see listTxPage.
func txPageLimit(pageSize int32) int32 {
	if pageSize <= 0 || pageSize > 100 {
		return 50
	}
	return pageSize
}

// exportPosting is a sqlx-scannable version of db.ExportPosting, less the
// correlations, which are read in a second pass and attached by posting id.
type exportPosting struct {
	Broker             string           `db:"broker"`
	ID                 string           `db:"id"`
	GroupID            string           `db:"group_id"`
	GroupTimestamp     time.Time        `db:"group_timestamp"`
	OrderDate          time.Time        `db:"order_date"`
	TradeDate          time.Time        `db:"trade_date"`
	Account            string           `db:"account"`
	AccountType        string           `db:"account_type"`
	BrokerTxTypes      pq.StringArray   `db:"broker_tx_type"`
	AssetClassHint     string           `db:"asset_class_hint"`
	Description        string           `db:"instrument_description"`
	IdentifierType     string           `db:"identifier_type"`
	IdentifierValue    string           `db:"value"`
	IdentifierDomain   string           `db:"domain"`
	Quantity           decimal.Decimal  `db:"quantity"`
	UnitPrice          *decimal.Decimal `db:"unit_price"`
	TradingCurrency    string           `db:"trading_currency"`
	SettlementCurrency string           `db:"settlement_currency"`
	SettlementAmount   *decimal.Decimal `db:"settlement_amount"`
}

// ListTxsForExport implements db.TxDB.
//
// The raw quantity and unit price travel, never the split-adjusted pair: those
// are a recomputable cache carrying a rounding, and the importing instance
// rebuilds them. Weights are left behind for the same reason -- they are
// computed at ingest from the raw columns and the tx type.
//
// The identifier join is a LEFT JOIN so a posting whose instrument never
// resolved survives, travelling with no identifier and resolving from its
// description on the way back in. bestIdentifierJoinOn rather than a
// hand-written lateral so this export agrees with every other one about which
// identifier is best.
func (p *Postgres) ListTxsForExport(ctx context.Context, userID string, periodFrom, periodBefore *timestamppb.Timestamp) ([]db.ExportPosting, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	args := []interface{}{userUUID}
	// The bounds filter the postings, not the groups that hold them: a period-scoped
	// export adheres to the period asked for, so a group straddling a bound
	// contributes only its in-period legs.
	period := ""
	if periodFrom != nil {
		from, err := tsToTime(periodFrom)
		if err != nil {
			return nil, fmt.Errorf("period_from: %w", err)
		}
		args = append(args, from)
		period += fmt.Sprintf("\n\t\t  AND t.order_date >= $%d", len(args))
	}
	if periodBefore != nil {
		before, err := tsToTime(periodBefore)
		if err != nil {
			return nil, fmt.Errorf("period_before: %w", err)
		}
		args = append(args, before)
		period += fmt.Sprintf("\n\t\t  AND t.order_date < $%d", len(args))
	}
	q := `
		SELECT t.broker, t.id::text AS id, t.group_id::text AS group_id, g.timestamp AS group_timestamp,
			t.order_date, t.trade_date, t.account, t.account_type, t.broker_tx_type,
			COALESCE(t.asset_class_hint, '') AS asset_class_hint, t.instrument_description,
			COALESCE(best_id.identifier_type, '') AS identifier_type,
			COALESCE(best_id.value, '') AS value,
			COALESCE(best_id.domain, '') AS domain,
			t.quantity, t.unit_price, t.settlement_amount,
			COALESCE(t.trading_currency, '') AS trading_currency,
			COALESCE(t.settlement_currency, '') AS settlement_currency
		FROM txs t
		JOIN tx_groups g ON g.id = t.group_id
		` + bestIdentifierJoinOn("LEFT JOIN", "t.instrument_id", "best_id") + `
		WHERE t.user_id = $1
		  -- Only what a source stated. A residual and a boundary leg are derived
		  -- from the group they sit in, and the file no longer carries the
		  -- partition that put them there, so a re-imported one would land in a
		  -- group of its own and be balanced by a counterparty of its own. They
		  -- are derived again on the way in, from the postings that are carried.
		  AND t.synthetic_purpose IS NULL
		  -- Excluded whole rather than per posting: a pad and its EQUITY
		  -- counterparty share one group, and half a group is not a group.
		  AND NOT EXISTS (
		    SELECT 1 FROM txs s
		    WHERE s.group_id = t.group_id
		      AND s.synthetic_purpose = '` + db.InitializePurpose + `'
		  )` + period + `
		ORDER BY t.broker, g.timestamp, g.id, t.order_date, t.id
	`
	var rows []exportPosting
	if err := p.q.SelectContext(ctx, &rows, q, args...); err != nil {
		return nil, fmt.Errorf("list txs for export: %w", err)
	}
	ids := make([]string, len(rows))
	out := make([]db.ExportPosting, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		out[i] = db.ExportPosting{
			Broker:             r.Broker,
			ID:                 r.ID,
			GroupID:            r.GroupID,
			GroupTimestamp:     r.GroupTimestamp,
			OrderDate:          r.OrderDate,
			TradeDate:          r.TradeDate,
			Account:            r.Account,
			AccountType:        r.AccountType,
			BrokerTxTypes:      r.BrokerTxTypes,
			AssetClassHint:     r.AssetClassHint,
			Description:        r.Description,
			IdentifierType:     r.IdentifierType,
			IdentifierValue:    r.IdentifierValue,
			IdentifierDomain:   r.IdentifierDomain,
			Quantity:           r.Quantity,
			UnitPrice:          r.UnitPrice,
			TradingCurrency:    r.TradingCurrency,
			SettlementCurrency: r.SettlementCurrency,
			SettlementAmount:   r.SettlementAmount,
		}
	}
	// A second pass rather than an aggregate in the scan above: the postings are
	// read as one ordered stream, and a lateral json_agg would make every row
	// carry a document to be decoded whether it correlates with anything or not.
	byTx, err := p.correlationsByTx(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Correlations = byTx[out[i].ID]
	}
	return out, nil
}

// correlationsByTx reads the correlations of the given postings, in the order
// their sources stated them.
func (p *Postgres) correlationsByTx(ctx context.Context, txIDs []string) (map[string][]db.Correlation, error) {
	if len(txIDs) == 0 {
		return nil, nil
	}
	const q = `
		SELECT tx_id::text AS tx_id, label, token, ordinal, scope, matches, ordinal_span
		FROM tx_correlations
		WHERE tx_id = ANY($1::uuid[])
		ORDER BY tx_id, ordinality
	`
	var rows []struct {
		TxID        string         `db:"tx_id"`
		Label       string         `db:"label"`
		Token       string         `db:"token"`
		Ordinal     *int64         `db:"ordinal"`
		Scope       string         `db:"scope"`
		Matches     pq.StringArray `db:"matches"`
		OrdinalSpan *int64         `db:"ordinal_span"`
	}
	if err := p.q.SelectContext(ctx, &rows, q, pq.Array(txIDs)); err != nil {
		return nil, fmt.Errorf("list tx correlations: %w", err)
	}
	out := make(map[string][]db.Correlation, len(rows))
	for _, r := range rows {
		out[r.TxID] = append(out[r.TxID], db.Correlation{
			Label:       r.Label,
			Token:       r.Token,
			Ordinal:     r.Ordinal,
			Scope:       r.Scope,
			Match:       []string(r.Matches),
			OrdinalSpan: r.OrdinalSpan,
		})
	}
	return out, nil
}

// routedPurpose is what synthetic_purpose says of a posting the server routed to
// balance a group. It is what tells a routed leg from a stated one, and the account
// type is not: a residual and a leg a converter read out of a record can land in the
// same account type.
//
// Both values here are re-derived together whenever a group's membership changes. A
// boundary leg is a function of one posting rather than of the group, but it has to
// move with the leg it mirrors, and re-deriving is how it does.
const routedPurpose = `'RESIDUAL', 'BOUNDARY'`

// survivorPredicate selects the postings of a touched group that a replace leaves
// standing, and is the one definition of that. $1 is the broker, $2 and $3 the period
// bounds.
//
// A routed counterparty never survives. Residuals are derived again against the legs a
// group ends with, so a group carries one per commodity however many replaces have
// reached it rather than a stack of them.
//
// Two kinds of posting do survive, and both were destroyed by the whole-group delete
// this replaces. A posting of another broker is not this upload's to replace. A
// synthetic INITIALIZE posting is the declaration machinery's rather than ingestion's.
const survivorPredicate = `
	(t.synthetic_purpose IS NULL OR t.synthetic_purpose NOT IN (` + routedPurpose + `))
	AND NOT (t.broker = $1 AND t.order_date >= $2 AND t.order_date < $3
	         AND t.synthetic_purpose IS NULL)
`

// touchedGroups is the set of groups a replace reaches: those holding a non-synthetic
// posting of this broker inside the period. $4 is the user.
const touchedGroups = `
	SELECT DISTINCT t.group_id
	FROM txs t
	WHERE t.user_id = $4
	  AND t.broker = $1
	  AND t.order_date >= $2 AND t.order_date < $3
	  AND t.synthetic_purpose IS NULL
`

// deleteReplacedPostingsSQL removes what a replace is replacing, and names the groups
// it reached so the statements below can finish the job on them.
//
// It deletes postings rather than whole groups. A group's postings need not share a
// timestamp -- the Fidelity deposit-run pass keys on reference proximity, and a run in
// the sample export settles across two days -- so a whole-group delete destroys legs
// outside the period, which the upload that triggered it does not carry and nothing
// re-inserts.
//
// The legs that survive are left exactly as they are, on the group they were already
// in. Nothing is rewritten, so nothing derived is derived again: the stored weight that
// carries the balance (docs/adr/0029-posting-weight-is-stored.md), the split adjustment
// carrying any corporate action since ingest.
const deleteReplacedPostingsSQL = `
	WITH touched AS (` + touchedGroups + `)
	DELETE FROM txs t
	USING touched
	WHERE t.group_id = touched.group_id
	  AND NOT (` + survivorPredicate + `)
	RETURNING t.group_id
`

// deleteEmptiedGroupsSQL removes the groups the delete emptied, which is every group
// that did not straddle the period. The whole-group delete this replaces left the same
// rows behind.
const deleteEmptiedGroupsSQL = `
	DELETE FROM tx_groups g
	WHERE g.id = ANY($1)
	  AND NOT EXISTS (SELECT 1 FROM txs t WHERE t.group_id = g.id)
`

// deleteTouchedMatchesSQL drops the transfer matches naming a touched group. A match is
// a link between two groups rather than a posting, and it used to cascade with the
// group a replace deleted; a group that survives with a different residual needs it
// dropped explicitly, or the link outlives the evidence for it. The matcher runs after
// ingest and rebuilds what still holds.
// See docs/adr/0037-transfer-matches-are-links-not-postings.md.
const deleteTouchedMatchesSQL = `
	DELETE FROM transfer_matches m
	WHERE m.from_group_id = ANY($1) OR m.to_group_id = ANY($1)
`

// repointGroupTimestampsSQL re-dates a group whose first leg the delete took.
// tx_groups.timestamp is the earliest order_date of the postings that name the group
// and is derived rather than data, so it has to follow the legs that are left. The
// order date rather than the trade date, because that is what a listing orders by.
const repointGroupTimestampsSQL = `
	UPDATE tx_groups g
	SET timestamp = s.first_timestamp
	FROM (
		SELECT group_id, MIN(order_date) AS first_timestamp
		FROM txs
		WHERE group_id = ANY($1)
		GROUP BY group_id
	) s
	WHERE g.id = s.group_id AND g.timestamp <> s.first_timestamp
`

// groupResidualsSQL returns what each group the delete touched has left over, with
// everything the counterparty that balances it is built from.
//
// The sum is over the stored weight per weight_commodity, which is exactly what
// check_tx_group_balance() checks and what routeResiduals accumulates -- so no weight
// rule is re-derived here and none is written in SQL. The account type the residual
// takes is chosen in Go, by the rule the ingest balancer uses.
//
// first is the group's earliest surviving posting, matching the balancer taking the
// group's first leg: the residual keeps the broker, account, date and tx type of the
// group it balances, so it stays attributable. commodity is the earliest surviving leg
// weighing in that commodity, which is where a residual in a security takes its
// instrument and description from.
//
// rounding is what the group's own prices could be out by, which the tolerance that
// classifies the residual is scaled by. A leg converted into the settlement currency
// weighs at quantity * price * contract size, so a price quoted to n decimal places
// carries an error of up to half of the last one per unit; weight / price is the
// units it was applied to, contract size included, which is why no join to
// instruments is needed for the multiplier. $2 is residual.PriceScaleFloor, passed in
// so the assumption has one home rather than being spelled here as well.
//
// weight <> quantity is what says a leg converted. A cash leg weighs its own
// quantity, and its price of 1 is exact by definition rather than a rounded quote, so
// it contributes nothing -- which is what leaves a deposit run's tolerance where it
// was. A price of zero contributes nothing either: an option expiring worthless
// converts at zero, so there is no consideration to have rounded.
const groupResidualsSQL = `
	SELECT r.group_id, r.weight_commodity, r.residual,
	       rounding.price_rounding,
	       types.resolved_tx_types,
	       first.order_date, first.trade_date, first.broker_tx_type, first.resolved_tx_type, first.broker, first.account,
	       first.trading_currency, first.settlement_currency,
	       commodity.instrument_description, commodity.instrument_id
	FROM (
		SELECT group_id, weight_commodity, SUM(weight) AS residual
		FROM txs
		WHERE group_id = ANY($1)
		GROUP BY group_id, weight_commodity
		HAVING SUM(weight) <> 0
	) r
	JOIN LATERAL (
		SELECT COALESCE(SUM(
			abs(t.weight / t.unit_price)
			* 0.5 * power(10::numeric, -greatest(scale(t.unit_price), $2::int))
		), 0) AS price_rounding
		FROM txs t
		WHERE t.group_id = r.group_id
		  AND t.unit_price IS NOT NULL AND t.unit_price <> 0
		  AND t.weight <> t.quantity
	) rounding ON true
	JOIN LATERAL (
		SELECT array_agg(DISTINCT t.resolved_tx_type) AS resolved_tx_types
		FROM txs t WHERE t.group_id = r.group_id
	) types ON true
	JOIN LATERAL (
		SELECT t.order_date, t.trade_date, t.broker_tx_type, t.resolved_tx_type, t.broker, t.account,
		       t.trading_currency, t.settlement_currency
		FROM txs t WHERE t.group_id = r.group_id
		ORDER BY t.order_date, t.id LIMIT 1
	) first ON true
	JOIN LATERAL (
		SELECT t.instrument_description, t.instrument_id
		FROM txs t
		WHERE t.group_id = r.group_id AND t.weight_commodity = r.weight_commodity
		ORDER BY t.order_date, t.id LIMIT 1
	) commodity ON true
	ORDER BY r.group_id, r.weight_commodity
`

// boundaryCandidatesSQL returns the stated postings of the touched groups that name
// where their own money came from or went to, with everything the leg mirroring them
// is built from.
//
// Only postings a source stated are candidates. A routed leg is already somebody's
// other side, and mirroring one would post the same money twice.
//
// The mirror is of the stored weight rather than the quantity, so it is read here and
// negated in Go: a priced leg weighs at its consideration, and mirroring the quantity
// would leave the group short by the difference.
const boundaryCandidatesSQL = `
	SELECT t.group_id, t.weight, t.weight_commodity, t.order_date, t.trade_date,
	       t.broker_tx_type, t.resolved_tx_type, t.broker, t.account,
	       t.trading_currency, t.settlement_currency, t.instrument_description,
	       t.instrument_id
	FROM txs t
	WHERE t.group_id = ANY($1)
	  AND t.synthetic_purpose IS NULL
	  AND t.account_type = 'USER'
	  AND t.weight <> 0
	ORDER BY t.order_date, t.id
`

// currencyInstrumentSQL resolves the seeded instrument a money residual is denominated
// in. It runs on the replace's own transaction rather than through
// FindInstrumentByIdentifier so that the whole operation is one statement stream.
const currencyInstrumentSQL = `
	SELECT instrument_id FROM instrument_identifiers
	WHERE identifier_type = 'CURRENCY' AND domain IS NULL AND value = $1
`

// groupResidual is one commodity a group the replace cut no longer balances in, and
// the group attributes the counterparty for it is built from.
type groupResidual struct {
	groupID   uuid.UUID
	commodity string
	amount    decimal.Decimal
	// The distinct resolved types of the group's surviving legs, for the
	// family rule; the first leg's declared set and resolved value are what
	// the routed counterparty carries.
	resolvedTypes pq.StringArray
	// What the group's own prices could be out by, which scales the tolerance the
	// residual is classified against. Zero for a group that moved only money.
	priceRounding decimal.Decimal
	// Both dates of the group's earliest leg, since a routed residual is that
	// group's own event and belongs on the same days as the legs it balances.
	orderDate      time.Time
	tradeDate      time.Time
	brokerTxTypes  pq.StringArray
	resolvedTxType string
	broker         string
	account        string
	trading        *string
	settlement     *string
	description    string
	instrumentID   *uuid.UUID
}

// boundaryCandidate is one stated posting that names the account its other side sits
// in, and the attributes the leg mirroring it is built from.
type boundaryCandidate struct {
	groupID   uuid.UUID
	weight    decimal.Decimal
	commodity string
	// Both of the mirrored posting's dates, because the leg is that posting's
	// other side and is the same event on the same days.
	orderDate      time.Time
	tradeDate      time.Time
	brokerTxTypes  pq.StringArray
	resolvedTxType string
	broker         string
	account        string
	trading        *string
	settlement     *string
	description    string
	instrumentID   *uuid.UUID
}

// routeBoundaries writes the other side of every posting in the touched groups whose
// own type names one: the income a dividend came from, the expense a charge went to.
//
// Per posting rather than per group, which is what keeps a dividend and a charge in
// one group from netting into a single leg whose account would then be a coin toss.
// See residual.Boundary.
func routeBoundaries(ctx context.Context, exec queryable, userUUID uuid.UUID, groupIDs interface{}) error {
	rows, err := exec.QueryContext(ctx, boundaryCandidatesSQL, groupIDs)
	if err != nil {
		return fmt.Errorf("boundary candidates: %w", err)
	}
	defer rows.Close()
	var cands []boundaryCandidate
	for rows.Next() {
		var c boundaryCandidate
		if err := rows.Scan(&c.groupID, &c.weight, &c.commodity, &c.orderDate, &c.tradeDate,
			&c.brokerTxTypes, &c.resolvedTxType, &c.broker, &c.account,
			&c.trading, &c.settlement, &c.description, &c.instrumentID); err != nil {
			return fmt.Errorf("boundary candidates: %w", err)
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("boundary candidates: %w", err)
	}

	byCurrency := map[string]uuid.UUID{}
	for _, c := range cands {
		acct, named := residual.Boundary(db.StrToTxType(c.resolvedTxType))
		if !named {
			continue
		}
		acctType, err := accountTypeToStr(acct)
		if err != nil {
			return err
		}
		amount := c.weight.Neg()
		desc, trading, settlement := c.description, c.trading, c.settlement
		instID := c.instrumentID
		if code, money := residual.CurrencyOf(c.commodity); money {
			id, err := currencyInstrument(ctx, exec, byCurrency, code)
			if err != nil {
				return err
			}
			instID, desc, trading, settlement = &id, code, &code, &code
		}
		var txID uuid.UUID
		if err := exec.QueryRowContext(ctx, insertPostingInGroupSQL,
			userUUID, c.broker, c.account, c.orderDate, desc,
			c.brokerTxTypes, c.resolvedTxType, nil, amount,
			trading, settlement, nil, nil, nullUUID(instID), acctType,
			db.BoundaryPurpose, amount, c.commodity, c.groupID, c.tradeDate,
		).Scan(&txID); err != nil {
			return fmt.Errorf("insert boundary posting: %w", err)
		}
	}
	return nil
}

// clearPeriod deletes the postings a replace covers and returns the groups it left
// standing, for the caller to settle.
//
// A group that straddles the period keeps its postings outside it and gains a routed
// counterparty for what those legs no longer balance to. The result is stable under
// repetition rather than identical to what came before: re-importing a period leaves
// the out-of-period legs where they were and the in-period legs in a new group, each
// balanced by a routed residual.
//
// The delete and the routed insert commit together, and check_tx_group_balance() is
// DEFERRABLE INITIALLY DEFERRED, so the group is never observed unbalanced.
func clearPeriod(ctx context.Context, exec queryable, userUUID uuid.UUID, broker string, fromT, beforeT time.Time) ([]uuid.UUID, error) {
	touched, err := deleteReplacedPostings(ctx, exec, userUUID, broker, fromT, beforeT)
	if err != nil {
		return nil, err
	}
	if len(touched) == 0 {
		return nil, nil
	}
	ids := pq.Array(touched)
	if _, err := exec.ExecContext(ctx, deleteEmptiedGroupsSQL, ids); err != nil {
		return nil, fmt.Errorf("delete emptied tx groups: %w", err)
	}
	if _, err := exec.ExecContext(ctx, deleteTouchedMatchesSQL, ids); err != nil {
		return nil, fmt.Errorf("delete transfer matches: %w", err)
	}
	if _, err := exec.ExecContext(ctx, repointGroupTimestampsSQL, ids); err != nil {
		return nil, fmt.Errorf("repoint tx group timestamps: %w", err)
	}
	return touched, nil
}

// deleteReplacedPostings removes what the replace is replacing and returns the distinct
// groups it reached.
func deleteReplacedPostings(ctx context.Context, exec queryable, userUUID uuid.UUID, broker string, fromT, beforeT time.Time) ([]uuid.UUID, error) {
	rows, err := exec.QueryContext(ctx, deleteReplacedPostingsSQL, broker, fromT, beforeT, userUUID)
	if err != nil {
		return nil, fmt.Errorf("delete txs in period: %w", err)
	}
	defer rows.Close()
	seen := map[uuid.UUID]bool{}
	var touched []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("delete txs in period: %w", err)
		}
		if !seen[id] {
			seen[id] = true
			touched = append(touched, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("delete txs in period: %w", err)
	}
	return touched, nil
}

// residualRule classifies what a group has left over. The two differ only in whether
// a difference small enough to be a rounding is read as one.
type residualRule func(commodity string, amount, priceRounding decimal.Decimal, resolved []typev1.TxType) typev1.AccountType

// wholeSource is the rule for a group holding every leg its source stated. What is
// left over is the source's own figures failing to reconcile, so a small enough
// difference is the source disagreeing with itself. See residual.Type.
func wholeSource(commodity string, amount, priceRounding decimal.Decimal, resolved []typev1.TxType) typev1.AccountType {
	return residual.Type(commodity, amount, priceRounding, resolved)
}

// legsRemoved is the rule for a group something was taken out of. What is left over
// is the value of the legs that went rather than two figures rounded differently, so
// it can be any size and a small one is small by coincidence. See residual.SplitType.
func legsRemoved(_ string, _, _ decimal.Decimal, resolved []typev1.TxType) typev1.AccountType {
	return residual.SplitType(resolved)
}

// settleSet is the groups one write touched, and which of them a posting left.
//
// A write can reach a group in several ways at once -- a replace cuts it, an insert
// adds to it, the engine moves a member out of it -- and what it is owed has to be
// decided once, from all of them together. Losing a leg is what decides the rule, so
// it wins over any other reason the group is here.
type settleSet struct {
	all  map[string]bool
	lost map[string]bool
}

func (s *settleSet) add(id string, lostLegs bool) {
	if s.all == nil {
		s.all = map[string]bool{}
		s.lost = map[string]bool{}
	}
	s.all[id] = true
	if lostLegs {
		s.lost[id] = true
	}
}

func (s *settleSet) addAll(ids []uuid.UUID, lostLegs bool) {
	for _, id := range ids {
		s.add(id.String(), lostLegs)
	}
}

// ids is every group the write touched, for the statements that treat them alike.
func (s *settleSet) ids() []string {
	out := make([]string, 0, len(s.all))
	for id := range s.all {
		out = append(out, id)
	}
	return out
}

// settle writes what each group is owed, under the rule its state calls for.
func (s *settleSet) settle(ctx context.Context, exec queryable, userUUID uuid.UUID) error {
	var whole, shortened []uuid.UUID
	for id := range s.all {
		u, err := uuid.Parse(id)
		if err != nil {
			return fmt.Errorf("invalid group id %q: %w", id, err)
		}
		if s.lost[id] {
			shortened = append(shortened, u)
		} else {
			whole = append(whole, u)
		}
	}
	if err := settle(ctx, exec, userUUID, whole, wholeSource); err != nil {
		return err
	}
	return settle(ctx, exec, userUUID, shortened, legsRemoved)
}

// settle writes the legs the server owes a set of groups, and is the one place that
// happens: after an upload is stored, after a replace cuts a group, and after a
// regroup moves a member.
//
// Boundary legs go first and residuals second, because a residual is what is left
// after every side the data names. Both are derived from the legs the group ends
// with, so a caller that changed a group's membership deletes them first and lets
// this write them again rather than trying to adjust them.
//
// The rule is the caller's because only the caller knows whether anything was taken
// out of these groups.
func settle(ctx context.Context, exec queryable, userUUID uuid.UUID, groupIDs []uuid.UUID, rule residualRule) error {
	if len(groupIDs) == 0 {
		return nil
	}
	ids := pq.Array(groupIDs)
	if err := routeBoundaries(ctx, exec, userUUID, ids); err != nil {
		return err
	}
	return routeResiduals(ctx, exec, userUUID, ids, rule)
}

// routeResiduals writes the counterparty that balances each group left unbalanced
// once its boundary legs are in.
func routeResiduals(ctx context.Context, exec queryable, userUUID uuid.UUID, groupIDs interface{}, rule residualRule) error {
	rows, err := exec.QueryContext(ctx, groupResidualsSQL, groupIDs, residual.PriceScaleFloor)
	if err != nil {
		return fmt.Errorf("surviving residuals: %w", err)
	}
	defer rows.Close()
	var residuals []groupResidual
	for rows.Next() {
		var r groupResidual
		if err := rows.Scan(&r.groupID, &r.commodity, &r.amount, &r.priceRounding, &r.resolvedTypes,
			&r.orderDate, &r.tradeDate, &r.brokerTxTypes, &r.resolvedTxType, &r.broker, &r.account,
			&r.trading, &r.settlement, &r.description, &r.instrumentID); err != nil {
			return fmt.Errorf("surviving residuals: %w", err)
		}
		residuals = append(residuals, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("surviving residuals: %w", err)
	}

	byCurrency := map[string]uuid.UUID{}
	for _, r := range residuals {
		amount := r.amount.Neg()
		acctType, err := accountTypeToStr(rule(r.commodity, amount, r.priceRounding, db.StrsToTxTypes(r.resolvedTypes)))
		if err != nil {
			return err
		}

		// A residual in a security carries the instrument and description of the legs
		// it balances; one in money resolves the seeded currency instrument and takes
		// the code as its description, matching how an ordinary cash row arrives so
		// nothing downstream has to treat it specially.
		desc, trading, settlement := r.description, r.trading, r.settlement
		instID := r.instrumentID
		if code, money := residual.CurrencyOf(r.commodity); money {
			id, err := currencyInstrument(ctx, exec, byCurrency, code)
			if err != nil {
				return err
			}
			instID, desc, trading, settlement = &id, code, &code, &code
		}

		// The split-adjusted pair is seeded from the raw values by the insert
		// trigger, exactly as for an uploaded posting. A routed leg has no source
		// row, so no
		// correlation and no settlement amount is written for it: it transcribes
		// nothing, so there is nothing the source said about why it belongs with
		// anything, and no figure of the source's to carry. It says so in
		// synthetic_purpose, which is what the next replace or regroup finds it by.
		// The returned id is read and dropped: the statement returns one because the
		// upload path needs it to hang correlations on.
		var txID uuid.UUID
		if err := exec.QueryRowContext(ctx, insertPostingInGroupSQL,
			userUUID, r.broker, r.account, r.orderDate, desc,
			r.brokerTxTypes, r.resolvedTxType, nil, amount,
			trading, settlement, nil, nil, nullUUID(instID), acctType,
			db.RoutedPurpose, amount, r.commodity, r.groupID, r.tradeDate,
		).Scan(&txID); err != nil {
			return fmt.Errorf("insert routed posting: %w", err)
		}
	}
	return nil
}

// currencyInstrument resolves and caches the seeded instrument a currency code names.
func currencyInstrument(ctx context.Context, exec queryable, cache map[string]uuid.UUID, code string) (uuid.UUID, error) {
	if id, ok := cache[code]; ok {
		return id, nil
	}
	var id uuid.UUID
	switch err := exec.QueryRowContext(ctx, currencyInstrumentSQL, code).Scan(&id); {
	case err == sql.ErrNoRows:
		// Currencies are seeded, so this is a safety net rather than a live path.
		// Naming the commodity is the only way the failure says anything useful: the
		// alternative is the deferred constraint rejecting the whole replace at COMMIT.
		return uuid.UUID{}, fmt.Errorf("no instrument for residual currency %q", code)
	case err != nil:
		return uuid.UUID{}, fmt.Errorf("resolve currency %s: %w", code, err)
	}
	cache[code] = id
	return id, nil
}
