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
	"google.golang.org/protobuf/proto"
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
		VALUES ($1, $4, $18)
		RETURNING id
	)
	INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
	                 broker_tx_type, resolved_tx_type, asset_class_hint,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 instrument_id, share_count_basis, account_type,
	                 weight, weight_commodity, group_id)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::date, $15, $16, $17, g.id FROM g
	RETURNING id, group_id
`

// insertPostingInGroupSQL inserts a tx as a posting of an existing tx group, for
// the second and subsequent legs of one economic event.
const insertPostingInGroupSQL = `
	INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
	                 broker_tx_type, resolved_tx_type, asset_class_hint,
	                 quantity, trading_currency, settlement_currency, unit_price,
	                 instrument_id, share_count_basis, account_type,
	                 weight, weight_commodity, group_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::date, $15, $16, $17, $18)
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

// groupResolver hands out the tx group for a tx's group_ref. An empty group_ref
// means the tx is its own single-posting group; a repeated one returns the group
// created for the first leg that used it. Refs are scoped to a single upload, so
// the resolver lives no longer than one call.
type groupResolver struct {
	byRef map[string]uuid.UUID
}

func newGroupResolver() *groupResolver {
	return &groupResolver{byRef: map[string]uuid.UUID{}}
}

// group returns the group id an already-created group should be reused for, and
// false when the caller must create one (an empty ref, or the first leg of a ref).
func (r *groupResolver) group(ref string) (uuid.UUID, bool) {
	if ref == "" {
		return uuid.UUID{}, false
	}
	id, ok := r.byRef[ref]
	return id, ok
}

// record remembers the group created for a non-empty ref so later legs join it.
func (r *groupResolver) record(ref string, id uuid.UUID) {
	if ref != "" {
		r.byRef[ref] = id
	}
}

// ReplaceTxsInPeriod implements db.TxDB.
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, userID, broker, jobID string, periodFrom, periodBefore *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight, shareCountBasis []*time.Time) error {
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
		if err := clearPeriod(ctx, exec, userUUID, broker, fromT, beforeT); err != nil {
			return err
		}
		return insertPostings(ctx, exec, userUUID, broker, jobUUID, txs, instrumentIDs, weights, shareCountBasis, "")
	})
}

// insertPostings writes each tx as a posting, creating one tx group per distinct
// group_ref and reusing it for that ref's later legs. account overrides the
// account on every posting when non-empty, for the append path where the whole
// group belongs to one named account.
//
// weights and shareCountBasis are parallel to txs, or nil when the caller has
// none. See db.TxDB for what a missing entry in either means.
func insertPostings(ctx context.Context, exec queryable, userUUID uuid.UUID, broker string, jobUUID interface{}, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight, shareCountBasis []*time.Time, account string) error {
	resolver := newGroupResolver()
	for i, t := range txs {
		instUUID, err := uuid.Parse(instrumentIDs[i])
		if err != nil {
			return fmt.Errorf("invalid instrument id: %w", err)
		}
		ts, err := tsToTime(t.Timestamp)
		if err != nil {
			return err
		}
		brokerTypes, err := db.TxTypesToStrs(t.GetBrokerTxType())
		if err != nil {
			return err
		}
		// Erroring on an unresolved value is what stops any path storing a
		// posting the ingest pipeline did not resolve.
		resolvedStr, err := txTypeToStr(t.GetResolvedTxType())
		if err != nil {
			return err
		}
		acctTypeStr, err := accountTypeToStr(t.GetAccountType())
		if err != nil {
			return err
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
			return fmt.Errorf("invalid quantity %q: %w", t.GetQuantity(), err)
		}
		price, err := parseOptDecimal(t.UnitPrice)
		if err != nil {
			return fmt.Errorf("invalid unit price %q: %w", t.GetUnitPrice(), err)
		}
		w := db.Weight{Amount: qty, Commodity: "inst:" + instUUID.String()}
		if i < len(weights) {
			w = weights[i]
		}
		// NULL leaves the basis to the insert trigger, which seeds it from the
		// posting's own timestamp -- the as-traded convention, and what all but a
		// restating source wants.
		var basis *time.Time
		if i < len(shareCountBasis) {
			basis = shareCountBasis[i]
		}
		args := []interface{}{
			userUUID, broker, acc, ts, t.InstrumentDescription,
			pq.Array(brokerTypes), resolvedStr, nullStr(db.AssetClassToStr(t.GetAssetClassHint())), qty,
			nullStr(t.TradingCurrency), nullStr(t.SettlementCurrency), nullDecimal(price),
			instUUID, basis, acctTypeStr, w.Amount, w.Commodity,
		}
		ref := t.GetGroupRef()
		var txID uuid.UUID
		if groupID, ok := resolver.group(ref); ok {
			if err := exec.QueryRowContext(ctx, insertPostingInGroupSQL, append(args, groupID)...).Scan(&txID); err != nil {
				return fmt.Errorf("insert tx: %w", err)
			}
		} else {
			// The group takes the timestamp of the first leg that names it.
			var groupID uuid.UUID
			if err := exec.QueryRowContext(ctx, insertPostingSQL, append(args, jobUUID)...).Scan(&txID, &groupID); err != nil {
				return fmt.Errorf("insert tx: %w", err)
			}
			resolver.record(ref, groupID)
		}
		if err := insertCorrelations(ctx, exec, txID, jobUUID, t.GetCorrelations()); err != nil {
			return err
		}
	}
	return nil
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

// CreateTxGroup implements db.TxDB. It appends the postings of one economic event
// as a single group, rather than one tx as a group of its own: a manually added
// trade that does not balance gets a routed counterparty like any other, and a
// group that could never be balanced would put the balance invariant permanently
// out of reach. The postings share the named account. group_ref is ignored -- the
// whole call is one group -- so the resolver never splits it.
func (p *Postgres) CreateTxGroup(ctx context.Context, userID, broker, account, jobID string, txs []*apiv1.Tx, instrumentIDs []string, weights []db.Weight, shareCountBasis []*time.Time) error {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("invalid user id: %w", err)
	}
	jobUUID, err := parseNullUUID(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	grouped := make([]*apiv1.Tx, len(txs))
	for i, t := range txs {
		grouped[i] = proto.CloneOf(t)
		grouped[i].GroupRef = "one-group"
	}
	return p.runInTx(ctx, func(exec queryable) error {
		if err := insertPostings(ctx, exec, userUUID, broker, jobUUID, grouped, instrumentIDs, weights, shareCountBasis, account); err != nil {
			return fmt.Errorf("create tx group: %w", err)
		}
		return nil
	})
}

// txOrderBy returns the ORDER BY clauses for a tx listing, with an id tiebreaker
// that makes the order total. Timestamps are not unique -- broker statements often
// supply only a date -- so without a tiebreaker the database is free to return
// tied rows in any order, and offset paging can then skip or repeat them across a
// page boundary.
func txOrderBy(prefix string, descending bool) []string {
	dir := ""
	if descending {
		dir = " DESC"
	}
	return []string{prefix + "timestamp" + dir, prefix + "id" + dir}
}

// ListTxs implements db.TxDB.
func (p *Postgres) ListTxs(ctx context.Context, userID string, broker *typev1.Broker, account string, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("broker", "account", "timestamp", "instrument_description", "broker_tx_type", "resolved_tx_type", "asset_class_hint", "quantity", "split_adjusted_quantity", "trading_currency", "settlement_currency", "unit_price", "split_adjusted_unit_price", "instrument_id", "synthetic_purpose", "account_type").
		From("txs").
		Where(sq.Eq{"user_id": userUUID}).
		OrderBy(txOrderBy("", descending)...)
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
	if periodBefore != nil {
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return nil, "", fmt.Errorf("period_before: %w", err)
		}
		qb = qb.Where(sq.Lt{"timestamp": beforeT})
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
func (p *Postgres) ListTxsByPortfolio(ctx context.Context, portfolioID string, broker *typev1.Broker, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	qb := psql.Select("t.broker", "t.account", "t.timestamp", "t.instrument_description", "t.broker_tx_type", "t.resolved_tx_type", "t.asset_class_hint", "t.quantity", "t.split_adjusted_quantity", "t.trading_currency", "t.settlement_currency", "t.unit_price", "t.split_adjusted_unit_price", "t.instrument_id", "t.synthetic_purpose", "t.account_type").
		From("txs t").
		Join("portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = ?", portUUID).
		OrderBy(txOrderBy("t.", descending)...)
	if broker != nil {
		brokerStr, err := brokerToStr(*broker)
		if err != nil {
			return nil, "", err
		}
		qb = qb.Where(sq.Eq{"t.broker": brokerStr})
	}
	if periodFrom != nil {
		fromT, err := tsToTime(periodFrom)
		if err != nil {
			return nil, "", fmt.Errorf("period_from: %w", err)
		}
		qb = qb.Where(sq.GtOrEq{"t.timestamp": fromT})
	}
	if periodBefore != nil {
		beforeT, err := tsToTime(periodBefore)
		if err != nil {
			return nil, "", fmt.Errorf("period_before: %w", err)
		}
		qb = qb.Where(sq.Lt{"t.timestamp": beforeT})
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

// exportPosting is a sqlx-scannable version of db.ExportPosting, less the
// correlations, which are read in a second pass and attached by posting id.
type exportPosting struct {
	Broker             string           `db:"broker"`
	ID                 string           `db:"id"`
	GroupID            string           `db:"group_id"`
	GroupTimestamp     time.Time        `db:"group_timestamp"`
	Timestamp          time.Time        `db:"timestamp"`
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
	ShareCountBasis    *time.Time       `db:"share_count_basis"`
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
		period += fmt.Sprintf("\n\t\t  AND t.timestamp >= $%d", len(args))
	}
	if periodBefore != nil {
		before, err := tsToTime(periodBefore)
		if err != nil {
			return nil, fmt.Errorf("period_before: %w", err)
		}
		args = append(args, before)
		period += fmt.Sprintf("\n\t\t  AND t.timestamp < $%d", len(args))
	}
	q := `
		SELECT t.broker, t.id::text AS id, t.group_id::text AS group_id, g.timestamp AS group_timestamp,
			t.timestamp, t.account, t.account_type, t.broker_tx_type,
			COALESCE(t.asset_class_hint, '') AS asset_class_hint, t.instrument_description,
			COALESCE(best_id.identifier_type, '') AS identifier_type,
			COALESCE(best_id.value, '') AS value,
			COALESCE(best_id.domain, '') AS domain,
			t.quantity, t.unit_price,
			COALESCE(t.trading_currency, '') AS trading_currency,
			COALESCE(t.settlement_currency, '') AS settlement_currency,
			-- A basis equal to the posting's own date is the as-traded
			-- convention and says nothing a reader cannot infer. The column is
			-- NOT NULL and the insert trigger defaults it to that date, so
			-- selecting it raw would stamp a redundant date onto every posting.
			CASE WHEN t.share_count_basis = t.timestamp::date THEN NULL
				ELSE t.share_count_basis END AS share_count_basis
		FROM txs t
		JOIN tx_groups g ON g.id = t.group_id
		` + bestIdentifierJoinOn("LEFT JOIN", "t.instrument_id", "best_id") + `
		WHERE t.user_id = $1
		  -- Excluded whole rather than per posting: a pad and its EQUITY
		  -- counterparty share one group, and half a group is not a group.
		  AND NOT EXISTS (
		    SELECT 1 FROM txs s
		    WHERE s.group_id = t.group_id AND s.synthetic_purpose IS NOT NULL
		  )` + period + `
		ORDER BY t.broker, g.timestamp, g.id, t.timestamp, t.id
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
			Timestamp:          r.Timestamp,
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
			ShareCountBasis:    r.ShareCountBasis,
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

// residualAccountTypes are the account types a routed counterparty takes.
const residualAccountTypes = `'IMBALANCE', 'TRANSFER_CLEARING', 'SOURCE_ROUNDING'`

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
	t.account_type NOT IN (` + residualAccountTypes + `)
	AND NOT (t.broker = $1 AND t.timestamp >= $2 AND t.timestamp < $3
	         AND t.synthetic_purpose IS NULL)
`

// touchedGroups is the set of groups a replace reaches: those holding a non-synthetic
// posting of this broker inside the period. $4 is the user.
const touchedGroups = `
	SELECT DISTINCT t.group_id
	FROM txs t
	WHERE t.user_id = $4
	  AND t.broker = $1
	  AND t.timestamp >= $2 AND t.timestamp < $3
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
// carrying any corporate action since ingest, and a restating source's
// share_count_basis.
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
// tx_groups.timestamp is the timestamp of the first posting that named the group and is
// derived rather than data, so it has to follow the legs that are left.
const repointGroupTimestampsSQL = `
	UPDATE tx_groups g
	SET timestamp = s.first_timestamp
	FROM (
		SELECT group_id, MIN(timestamp) AS first_timestamp
		FROM txs
		WHERE group_id = ANY($1)
		GROUP BY group_id
	) s
	WHERE g.id = s.group_id AND g.timestamp <> s.first_timestamp
`

// survivingResidualsSQL returns what each group the delete touched has left over, with
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
const survivingResidualsSQL = `
	SELECT r.group_id, r.weight_commodity, r.residual,
	       types.resolved_tx_types,
	       first.timestamp, first.broker_tx_type, first.resolved_tx_type, first.broker, first.account,
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
		SELECT array_agg(DISTINCT t.resolved_tx_type) AS resolved_tx_types
		FROM txs t WHERE t.group_id = r.group_id
	) types ON true
	JOIN LATERAL (
		SELECT t.timestamp, t.broker_tx_type, t.resolved_tx_type, t.broker, t.account,
		       t.trading_currency, t.settlement_currency
		FROM txs t WHERE t.group_id = r.group_id
		ORDER BY t.timestamp, t.id LIMIT 1
	) first ON true
	JOIN LATERAL (
		SELECT t.instrument_description, t.instrument_id
		FROM txs t
		WHERE t.group_id = r.group_id AND t.weight_commodity = r.weight_commodity
		ORDER BY t.timestamp, t.id LIMIT 1
	) commodity ON true
	ORDER BY r.group_id, r.weight_commodity
`

// currencyInstrumentSQL resolves the seeded instrument a money residual is denominated
// in. It runs on the replace's own transaction rather than through
// FindInstrumentByIdentifier so that the whole operation is one statement stream.
const currencyInstrumentSQL = `
	SELECT instrument_id FROM instrument_identifiers
	WHERE identifier_type = 'CURRENCY' AND domain IS NULL AND value = $1
`

// survivingResidual is one commodity a group the replace cut no longer balances in, and
// the group attributes the counterparty for it is built from.
type survivingResidual struct {
	groupID   uuid.UUID
	commodity string
	amount    decimal.Decimal
	// The distinct resolved types of the group's surviving legs, for the
	// family rule; the first leg's declared set and resolved value are what
	// the routed counterparty carries.
	resolvedTypes  pq.StringArray
	timestamp      time.Time
	brokerTxTypes  pq.StringArray
	resolvedTxType string
	broker         string
	account        string
	trading        *string
	settlement     *string
	description    string
	instrumentID   *uuid.UUID
}

// clearPeriod deletes the postings a replace covers and re-balances the groups it left
// standing.
//
// A group that straddles the period keeps its postings outside it and gains a routed
// counterparty for what those legs no longer balance to. The result is stable under
// repetition rather than identical to what came before: re-importing a period leaves
// the out-of-period legs where they were and the in-period legs in a new group, each
// balanced by a routed residual.
//
// The delete and the routed insert commit together, and check_tx_group_balance() is
// DEFERRABLE INITIALLY DEFERRED, so the group is never observed unbalanced.
func clearPeriod(ctx context.Context, exec queryable, userUUID uuid.UUID, broker string, fromT, beforeT time.Time) error {
	touched, err := deleteReplacedPostings(ctx, exec, userUUID, broker, fromT, beforeT)
	if err != nil {
		return err
	}
	if len(touched) == 0 {
		return nil
	}
	ids := pq.Array(touched)
	if _, err := exec.ExecContext(ctx, deleteEmptiedGroupsSQL, ids); err != nil {
		return fmt.Errorf("delete emptied tx groups: %w", err)
	}
	if _, err := exec.ExecContext(ctx, deleteTouchedMatchesSQL, ids); err != nil {
		return fmt.Errorf("delete transfer matches: %w", err)
	}
	if _, err := exec.ExecContext(ctx, repointGroupTimestampsSQL, ids); err != nil {
		return fmt.Errorf("repoint tx group timestamps: %w", err)
	}
	return routeSurvivors(ctx, exec, userUUID, ids)
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

// routeSurvivors writes the counterparty that balances each group the delete left
// unbalanced, by the family rule the ingest balancer applies to an upload. The
// tolerance is not applied: see residual.SplitType.
func routeSurvivors(ctx context.Context, exec queryable, userUUID uuid.UUID, groupIDs interface{}) error {
	rows, err := exec.QueryContext(ctx, survivingResidualsSQL, groupIDs)
	if err != nil {
		return fmt.Errorf("surviving residuals: %w", err)
	}
	defer rows.Close()
	var residuals []survivingResidual
	for rows.Next() {
		var r survivingResidual
		if err := rows.Scan(&r.groupID, &r.commodity, &r.amount, &r.resolvedTypes,
			&r.timestamp, &r.brokerTxTypes, &r.resolvedTxType, &r.broker, &r.account,
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
		acctType, err := accountTypeToStr(residual.SplitType(db.StrsToTxTypes(r.resolvedTypes)))
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

		// NULL share_count_basis leaves the insert trigger to seed it from the
		// posting's own timestamp, and the split-adjusted pair is seeded the same way,
		// exactly as for an uploaded posting. A routed leg has no source row, so no
		// correlation is written for it: it transcribes nothing and there is nothing
		// the source said about why it belongs with anything. The returned id is
		// read and dropped: the statement returns one because the upload path
		// needs it to hang correlations on.
		var txID uuid.UUID
		if err := exec.QueryRowContext(ctx, insertPostingInGroupSQL,
			userUUID, r.broker, r.account, r.timestamp, desc,
			r.brokerTxTypes, r.resolvedTxType, nil, amount,
			trading, settlement, nil, nullUUID(instID), nil, acctType,
			amount, r.commodity, r.groupID,
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
