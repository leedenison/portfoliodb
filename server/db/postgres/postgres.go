package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// errIdentifierExists is returned when EnsureInstrument hits a unique violation (identifier already for another instrument).
var errIdentifierExists = errors.New("identifier already exists for another instrument")

// mergeInstruments merges mergedAway into survivor inside the same transaction: updates all txs pointing at mergedAway to survivor, moves identifier rows to survivor (or keeps survivor's if duplicate), then deletes mergedAway. exec must be a transaction.
func mergeInstruments(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) error {
	if survivor == mergedAway {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `UPDATE txs SET instrument_id = $1 WHERE instrument_id = $2`, survivor, mergedAway); err != nil {
		return fmt.Errorf("update txs: %w", err)
	}
	rows, err := exec.QueryContext(ctx, `SELECT identifier_type, value, canonical FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway)
	if err != nil {
		return fmt.Errorf("list identifiers: %w", err)
	}
	defer rows.Close()
	var toInsert []struct{ idType, value string; canonical bool }
	for rows.Next() {
		var idType, val string
		var canonical bool
		if err := rows.Scan(&idType, &val, &canonical); err != nil {
			return err
		}
		toInsert = append(toInsert, struct{ idType, value string; canonical bool }{idType, val, canonical})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged identifiers: %w", err)
	}
	for _, idn := range toInsert {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical) VALUES ($1, $2, $3, $4)
			ON CONFLICT (identifier_type, value) DO NOTHING
		`, survivor, idn.idType, idn.value, idn.canonical)
		if err != nil {
			return fmt.Errorf("insert identifier: %w", err)
		}
	}
	// Update any instruments that referenced mergedAway as their underlying.
	if _, err := exec.ExecContext(ctx, `UPDATE instruments SET underlying_id = $1 WHERE underlying_id = $2`, survivor, mergedAway); err != nil {
		return fmt.Errorf("update instruments.underlying_id: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged instrument: %w", err)
	}
	return nil
}

// pickSurvivor returns the instrument ID that should survive when merging the given set (most identifiers, then oldest created_at). ids must have at least one element.
func pickSurvivor(ctx context.Context, q queryable, ids []uuid.UUID) (uuid.UUID, error) {
	if len(ids) == 0 {
		return uuid.Nil, fmt.Errorf("pickSurvivor requires at least one id")
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	// Build placeholders for IN clause to avoid pq.Array uuid handling
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ids[i]
	}
	inClause := strings.Join(placeholders, ",")
	query := fmt.Sprintf(`
		SELECT i.id, i.created_at, (SELECT count(*) FROM instrument_identifiers WHERE instrument_id = i.id) AS n
		FROM instruments i WHERE i.id IN (%s)
	`, inClause)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return uuid.Nil, fmt.Errorf("query instruments: %w", err)
	}
	defer rows.Close()
	type cand struct {
		id        uuid.UUID
		createdAt time.Time
		n         int64
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.createdAt, &c.n); err != nil {
			return uuid.Nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if len(cands) == 0 {
		return uuid.Nil, fmt.Errorf("no instruments found for ids")
	}
	// Sort by n desc, created_at asc (more identifiers wins, then older wins)
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].createdAt.Before(cands[j].createdAt)
	})
	return cands[0].id, nil
}

// queryable is satisfied by *sql.DB and *sql.Tx. Used so tests can run against a transaction that is rolled back.
type queryable interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
}

// Postgres implements db.DB using PostgreSQL.
type Postgres struct {
	q queryable
}

// New returns a new Postgres DB implementation.
func New(conn *sql.DB) *Postgres {
	return &Postgres{q: conn}
}

// NewWithQueryable returns a Postgres that uses the given queryable (e.g. *sql.Tx for tests). Callers must ensure the queryable is not closed while in use.
func NewWithQueryable(q queryable) *Postgres {
	return &Postgres{q: q}
}

// Ensure Postgres implements db.DB.
var _ db.DB = (*Postgres)(nil)

func brokerToStr(b apiv1.Broker) (string, error) {
	switch b {
	case apiv1.Broker_IBKR:
		return "IBKR", nil
	case apiv1.Broker_SCHB:
		return "SCHB", nil
	default:
		return "", fmt.Errorf("unknown broker: %v", b)
	}
}

func strToBroker(s string) apiv1.Broker {
	switch s {
	case "IBKR":
		return apiv1.Broker_IBKR
	case "SCHB":
		return apiv1.Broker_SCHB
	default:
		return apiv1.Broker_BROKER_UNSPECIFIED
	}
}

func txTypeToStr(t apiv1.TxType) (string, error) {
	if t == apiv1.TxType_TX_TYPE_UNSPECIFIED {
		return "", fmt.Errorf("tx type unspecified")
	}
	s := t.String()
	if s == "TX_TYPE_UNSPECIFIED" {
		return "", fmt.Errorf("tx type unspecified")
	}
	return s, nil
}

func strToTxType(s string) apiv1.TxType {
	v, ok := apiv1.TxType_value[s]
	if !ok {
		return apiv1.TxType_TX_TYPE_UNSPECIFIED
	}
	return apiv1.TxType(v)
}

func jobStatusToStr(s apiv1.JobStatus) string {
	switch s {
	case apiv1.JobStatus_PENDING:
		return "PENDING"
	case apiv1.JobStatus_RUNNING:
		return "RUNNING"
	case apiv1.JobStatus_SUCCESS:
		return "SUCCESS"
	case apiv1.JobStatus_FAILED:
		return "FAILED"
	default:
		return "PENDING"
	}
}

func strToJobStatus(s string) apiv1.JobStatus {
	switch s {
	case "PENDING":
		return apiv1.JobStatus_PENDING
	case "RUNNING":
		return apiv1.JobStatus_RUNNING
	case "SUCCESS":
		return apiv1.JobStatus_SUCCESS
	case "FAILED":
		return apiv1.JobStatus_FAILED
	default:
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

func tsToTime(ts *timestamppb.Timestamp) (time.Time, error) {
	if ts == nil || !ts.IsValid() {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}
	return ts.AsTime(), nil
}

func timeToTs(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

// GetOrCreateUser implements db.UserDB.
func (p *Postgres) GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error) {
	var id uuid.UUID
	err := p.q.QueryRowContext(ctx, `
		INSERT INTO users (auth_sub, name, email)
		VALUES ($1, $2, $3)
		ON CONFLICT (auth_sub) DO UPDATE SET name = $2, email = $3
		RETURNING id
	`, authSub, name, email).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("get or create user: %w", err)
	}
	return id.String(), nil
}

// GetUserByAuthSub implements db.UserDB.
func (p *Postgres) GetUserByAuthSub(ctx context.Context, authSub string) (userID, role string, err error) {
	var id uuid.UUID
	var roleVal string
	queryErr := p.q.QueryRowContext(ctx, `SELECT id, role FROM users WHERE auth_sub = $1`, authSub).Scan(&id, &roleVal)
	if queryErr == sql.ErrNoRows {
		return "", "", nil
	}
	if queryErr != nil {
		return "", "", fmt.Errorf("get user by auth sub: %w", queryErr)
	}
	return id.String(), roleVal, nil
}

// ListPortfolios implements db.PortfolioDB.
func (p *Postgres) ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := int64(0)
	if pageToken != "" {
		b, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			offset, _ = strconv.ParseInt(string(b), 10, 64)
		}
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT id, name, created_at FROM portfolios
		WHERE user_id = $1
		ORDER BY created_at
		LIMIT $2 OFFSET $3
	`, userUUID, limit+1, offset)
	if err != nil {
		return nil, "", fmt.Errorf("list portfolios: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Portfolio
	var n int32
	for rows.Next() && n < limit {
		var id uuid.UUID
		var name string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			return nil, "", err
		}
		out = append(out, &apiv1.Portfolio{
			Id:        id.String(),
			Name:      name,
			CreatedAt: timeToTs(createdAt),
		})
		n++
	}
	var nextToken string
	if n == limit+1 || (rows.Next() && n == limit) {
		nextToken = base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(offset+int64(limit), 10)))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return out, nextToken, nil
}

// GetPortfolio implements db.PortfolioDB.
func (p *Postgres) GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	var id uuid.UUID
	var userID uuid.UUID
	var name string
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		SELECT id, user_id, name, created_at FROM portfolios WHERE id = $1
	`, portUUID).Scan(&id, &userID, &name, &createdAt)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        id.String(),
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, userID.String(), nil
}

// CreatePortfolio implements db.PortfolioDB.
func (p *Postgres) CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	var id uuid.UUID
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		INSERT INTO portfolios (user_id, name) VALUES ($1, $2)
		RETURNING id, created_at
	`, userUUID, name).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("create portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        id.String(),
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, nil
}

// UpdatePortfolio implements db.PortfolioDB.
func (p *Postgres) UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	var createdAt time.Time
	err = p.q.QueryRowContext(ctx, `
		UPDATE portfolios SET name = $2 WHERE id = $1
		RETURNING created_at
	`, portUUID, name).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update portfolio: %w", err)
	}
	return &apiv1.Portfolio{
		Id:        portfolioID,
		Name:      name,
		CreatedAt: timeToTs(createdAt),
	}, nil
}

// DeletePortfolio implements db.PortfolioDB.
func (p *Postgres) DeletePortfolio(ctx context.Context, portfolioID string) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
	}
	res, err := p.q.ExecContext(ctx, `DELETE FROM portfolios WHERE id = $1`, portUUID)
	if err != nil {
		return fmt.Errorf("delete portfolio: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	return nil
}

// PortfolioBelongsToUser implements db.PortfolioDB.
func (p *Postgres) PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return false, fmt.Errorf("invalid portfolio id: %w", err)
	}
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return false, fmt.Errorf("invalid user id: %w", err)
	}
	var count int
	err = p.q.QueryRowContext(ctx, `SELECT 1 FROM portfolios WHERE id = $1 AND user_id = $2`, portUUID, userUUID).Scan(&count)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("portfolio belongs to user: %w", err)
	}
	return true, nil
}

// runInTx runs f inside a transaction. When p.q is *sql.DB it begins a new tx, runs f, and commits; when p.q is *sql.Tx (e.g. in tests) it runs f on that tx and does not commit.
func (p *Postgres) runInTx(ctx context.Context, f func(exec queryable) error) error {
	switch q := p.q.(type) {
	case *sql.Tx:
		return f(q)
	case *sql.DB:
		tx, err := q.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := f(tx); err != nil {
			return err
		}
		return tx.Commit()
	default:
		return fmt.Errorf("unsupported queryable type %T", p.q)
	}
}

// ReplaceTxsInPeriod implements db.TxDB.
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
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
			DELETE FROM txs WHERE portfolio_id = $1 AND broker = $2 AND timestamp >= $3 AND timestamp <= $4
		`, portUUID, broker, fromT, toT)
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
			_, err = exec.ExecContext(ctx, `
				INSERT INTO txs (portfolio_id, broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			`, portUUID, broker, ts, t.InstrumentDescription, txTypeStr, t.Quantity, nullStr(t.Currency), nullFloat(t.UnitPrice), instUUID)
			if err != nil {
				return fmt.Errorf("insert tx: %w", err)
			}
		}
		return nil
	})
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(u *uuid.UUID) interface{} {
	if u == nil {
		return nil
	}
	return *u
}

func nullTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

// strFromNull returns s.String if s.Valid, otherwise "".
func strFromNull(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// timeFromNull returns t.Time if t.Valid, otherwise nil.
func timeFromNull(t sql.NullTime) *time.Time {
	if t.Valid {
		return &t.Time
	}
	return nil
}

func nullFloat(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

// UpsertTx implements db.TxDB.
func (p *Postgres) UpsertTx(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx, instrumentID string) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
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
		INSERT INTO txs (portfolio_id, broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (portfolio_id, broker, timestamp, instrument_description)
		DO UPDATE SET tx_type = EXCLUDED.tx_type, quantity = EXCLUDED.quantity, currency = EXCLUDED.currency, unit_price = EXCLUDED.unit_price, instrument_id = EXCLUDED.instrument_id
	`, portUUID, broker, ts, tx.InstrumentDescription, txTypeStr, tx.Quantity, nullStr(tx.Currency), nullFloat(tx.UnitPrice), instUUID)
	if err != nil {
		return fmt.Errorf("upsert tx: %w", err)
	}
	return nil
}

// ListTxs implements db.TxDB.
func (p *Postgres) ListTxs(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := `
		SELECT broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price, instrument_id
		FROM txs WHERE portfolio_id = $1
	`
	args := []interface{}{portUUID}
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
		var brokerStr string
		var ts time.Time
		var instDesc, txTypeStr string
		var qty float64
		var currency sql.NullString
		var unitPrice sql.NullFloat64
		var instrumentID sql.NullString
		if err := rows.Scan(&brokerStr, &ts, &instDesc, &txTypeStr, &qty, &currency, &unitPrice, &instrumentID); err != nil {
			return nil, "", err
		}
		tx := &apiv1.Tx{
			Timestamp:             timeToTs(ts),
			InstrumentDescription: instDesc,
			Type:                  strToTxType(txTypeStr),
			Quantity:              qty,
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
			Broker: strToBroker(brokerStr),
			Tx:     tx,
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

// ComputeHoldings implements db.HoldingsDB.
func (p *Postgres) ComputeHoldings(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	asOfT := time.Now().UTC()
	if asOf != nil && asOf.IsValid() {
		asOfT = asOf.AsTime()
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT broker, instrument_description, instrument_id,
			SUM(quantity) AS quantity
		FROM txs
		WHERE portfolio_id = $1 AND timestamp <= $2
		GROUP BY portfolio_id, broker, instrument_description, instrument_id
		HAVING SUM(quantity) != 0
	`, portUUID, asOfT)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Holding
	for rows.Next() {
		var brokerStr string
		var instDesc string
		var instrumentID sql.NullString
		var qty float64
		if err := rows.Scan(&brokerStr, &instDesc, &instrumentID, &qty); err != nil {
			return nil, nil, err
		}
		h := &apiv1.Holding{
			Broker:                strToBroker(brokerStr),
			InstrumentDescription: instDesc,
			Quantity:              qty,
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

// CreateJob implements db.JobDB.
func (p *Postgres) CreateJob(ctx context.Context, portfolioID, broker, source string, periodFrom, periodTo *timestamppb.Timestamp) (string, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return "", fmt.Errorf("invalid portfolio id: %w", err)
	}
	var fromT, toT interface{}
	if periodFrom != nil && periodFrom.IsValid() {
		fromT = periodFrom.AsTime()
	}
	if periodTo != nil && periodTo.IsValid() {
		toT = periodTo.AsTime()
	}
	var id uuid.UUID
	err = p.q.QueryRowContext(ctx, `
		INSERT INTO ingestion_jobs (portfolio_id, broker, source, period_from, period_to, status)
		VALUES ($1, $2, $3, $4, $5, 'PENDING')
		RETURNING id
	`, portUUID, broker, source, fromT, toT).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	return id.String(), nil
}

// GetJob implements db.JobDB.
func (p *Postgres) GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, []db.IdentificationError, string, error) {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", fmt.Errorf("invalid job id: %w", err)
	}
	var statusStr string
	var portfolioID uuid.UUID
	err = p.q.QueryRowContext(ctx, `SELECT status, portfolio_id FROM ingestion_jobs WHERE id = $1`, jobUUID).Scan(&statusStr, &portfolioID)
	if err == sql.ErrNoRows {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", nil
	}
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", fmt.Errorf("get job: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `SELECT row_index, field, message FROM validation_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", fmt.Errorf("get validation errors: %w", err)
	}
	defer rows.Close()
	var errs []*apiv1.ValidationError
	for rows.Next() {
		var rowIndex int32
		var field, message string
		if err := rows.Scan(&rowIndex, &field, &message); err != nil {
			return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", err
		}
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: field, Message: message})
	}
	if err := rows.Err(); err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", err
	}
	// Identification errors
	idRows, err := p.q.QueryContext(ctx, `SELECT row_index, instrument_description, message FROM identification_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", fmt.Errorf("get identification errors: %w", err)
	}
	defer idRows.Close()
	var idErrs []db.IdentificationError
	for idRows.Next() {
		var e db.IdentificationError
		if err := idRows.Scan(&e.RowIndex, &e.InstrumentDescription, &e.Message); err != nil {
			return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", err
		}
		idErrs = append(idErrs, e)
	}
	if err := idRows.Err(); err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", err
	}
	return strToJobStatus(statusStr), errs, idErrs, portfolioID.String(), nil
}

// SetJobStatus implements db.JobDB.
func (p *Postgres) SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET status = $2 WHERE id = $1`, jobUUID, jobStatusToStr(status))
	if err != nil {
		return fmt.Errorf("set job status: %w", err)
	}
	return nil
}

// AppendValidationErrors implements db.JobDB.
func (p *Postgres) AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	for _, e := range errs {
		_, err = p.q.ExecContext(ctx, `INSERT INTO validation_errors (job_id, row_index, field, message) VALUES ($1, $2, $3, $4)`,
			jobUUID, e.RowIndex, e.Field, e.Message)
		if err != nil {
			return fmt.Errorf("append validation error: %w", err)
		}
	}
	return nil
}

// AppendIdentificationErrors implements db.JobDB.
func (p *Postgres) AppendIdentificationErrors(ctx context.Context, jobID string, errs []db.IdentificationError) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	for _, e := range errs {
		_, err = p.q.ExecContext(ctx, `INSERT INTO identification_errors (job_id, row_index, instrument_description, message) VALUES ($1, $2, $3, $4)`,
			jobUUID, e.RowIndex, e.InstrumentDescription, e.Message)
		if err != nil {
			return fmt.Errorf("append identification error: %w", err)
		}
	}
	return nil
}

// ListPendingJobIDs implements db.JobDB.
func (p *Postgres) ListPendingJobIDs(ctx context.Context) ([]string, error) {
	rows, err := p.q.QueryContext(ctx, `SELECT id FROM ingestion_jobs WHERE status = 'PENDING' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending jobs: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id.String())
	}
	return ids, rows.Err()
}

// FindInstrumentByIdentifier implements db.InstrumentDB.
func (p *Postgres) FindInstrumentByIdentifier(ctx context.Context, identifierType, value string) (string, error) {
	var id uuid.UUID
	err := p.q.QueryRowContext(ctx, `
		SELECT instrument_id FROM instrument_identifiers
		WHERE identifier_type = $1 AND value = $2
	`, identifierType, value).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find instrument by identifier: %w", err)
	}
	return id.String(), nil
}

// GetInstrument implements db.InstrumentDB.
func (p *Postgres) GetInstrument(ctx context.Context, instrumentID string) (*db.InstrumentRow, error) {
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("invalid instrument id: %w", err)
	}
	var row db.InstrumentRow
	row.ID = instrumentID
	var assetClass, exchange, currency, name sql.NullString
	var underlyingID sql.NullString
	var validFrom, validTo sql.NullTime
	err = p.q.QueryRowContext(ctx, `SELECT asset_class, exchange, currency, name, underlying_id, valid_from, valid_to FROM instruments WHERE id = $1`, instUUID).
		Scan(&assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instrument: %w", err)
	}
	row.AssetClass = strFromNull(assetClass)
	row.Exchange = strFromNull(exchange)
	row.Currency = strFromNull(currency)
	row.Name = strFromNull(name)
	row.UnderlyingID = strFromNull(underlyingID)
	row.ValidFrom = timeFromNull(validFrom)
	row.ValidTo = timeFromNull(validTo)
	idRows, err := p.q.QueryContext(ctx, `SELECT identifier_type, value, canonical FROM instrument_identifiers WHERE instrument_id = $1`, instUUID)
	if err != nil {
		return nil, fmt.Errorf("get instrument identifiers: %w", err)
	}
	defer idRows.Close()
	for idRows.Next() {
		var idn db.IdentifierInput
		if err := idRows.Scan(&idn.Type, &idn.Value, &idn.Canonical); err != nil {
			return nil, err
		}
		row.Identifiers = append(row.Identifiers, idn)
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}
	return &row, nil
}

// ListEnabledPluginConfigs implements db.InstrumentDB.
func (p *Postgres) ListEnabledPluginConfigs(ctx context.Context) ([]db.PluginConfigRow, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT plugin_id, precedence, config FROM identifier_plugin_config
		WHERE enabled = true ORDER BY precedence DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled plugin configs: %w", err)
	}
	defer rows.Close()
	var out []db.PluginConfigRow
	for rows.Next() {
		var r db.PluginConfigRow
		var config sql.NullString
		if err := rows.Scan(&r.PluginID, &r.Precedence, &config); err != nil {
			return nil, err
		}
		if config.Valid {
			r.Config = []byte(config.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListInstrumentsForExport implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*db.InstrumentRow, error) {
	var rows *sql.Rows
	var err error
	if exchangeFilter != "" {
		rows, err = p.q.QueryContext(ctx, `
			SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
			FROM instruments i
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			AND i.exchange = $1
			ORDER BY i.id
		`, exchangeFilter)
	} else {
		rows, err = p.q.QueryContext(ctx, `
			SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
			FROM instruments i
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			ORDER BY i.id
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("list instruments for export: %w", err)
	}
	defer rows.Close()
	var results []*db.InstrumentRow
	var ids []uuid.UUID
	for rows.Next() {
		var row db.InstrumentRow
		var id uuid.UUID
		var assetClass, exchange, currency, name sql.NullString
		var underlyingID sql.NullString
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
			return nil, err
		}
		row.ID = id.String()
		row.AssetClass = strFromNull(assetClass)
		row.Exchange = strFromNull(exchange)
		row.Currency = strFromNull(currency)
		row.Name = strFromNull(name)
		row.UnderlyingID = strFromNull(underlyingID)
		row.ValidFrom = timeFromNull(validFrom)
		row.ValidTo = timeFromNull(validTo)
		results = append(results, &row)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return results, nil
	}
	// Load all identifiers for these instruments (build placeholders to avoid pq.Array uuid handling).
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ids[i]
	}
	idRows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, value, canonical
		FROM instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("list identifiers for export: %w", err)
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow)
	for _, r := range results {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &val, &canonical); err != nil {
			return nil, err
		}
		row := byID[instID.String()]
		if row != nil {
			row.Identifiers = append(row.Identifiers, db.IdentifierInput{Type: idType, Value: val, Canonical: canonical})
		}
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ListInstrumentsByIDs implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsByIDs(ctx context.Context, ids []string) ([]*db.InstrumentRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var uuids []uuid.UUID
	for _, s := range ids {
		if s == "" || seen[s] {
			continue
		}
		parsed, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		seen[s] = true
		uuids = append(uuids, parsed)
	}
	if len(uuids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(uuids))
	args := make([]interface{}, len(uuids))
	for i := range uuids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = uuids[i]
	}
	rows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
		FROM instruments i WHERE i.id IN (%s)
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("list instruments by ids: %w", err)
	}
	defer rows.Close()
	var results []*db.InstrumentRow
	var resultIDs []uuid.UUID
	for rows.Next() {
		var row db.InstrumentRow
		var id uuid.UUID
		var assetClass, exchange, currency, name sql.NullString
		var underlyingID sql.NullString
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
			return nil, err
		}
		row.ID = id.String()
		row.AssetClass = strFromNull(assetClass)
		row.Exchange = strFromNull(exchange)
		row.Currency = strFromNull(currency)
		row.Name = strFromNull(name)
		row.UnderlyingID = strFromNull(underlyingID)
		row.ValidFrom = timeFromNull(validFrom)
		row.ValidTo = timeFromNull(validTo)
		results = append(results, &row)
		resultIDs = append(resultIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(resultIDs) == 0 {
		return results, nil
	}
	// Load identifiers for these instruments.
	idPlaceholders := make([]string, len(resultIDs))
	idArgs := make([]interface{}, len(resultIDs))
	for i := range resultIDs {
		idPlaceholders[i] = fmt.Sprintf("$%d", i+1)
		idArgs[i] = resultIDs[i]
	}
	idRows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, value, canonical
		FROM instrument_identifiers WHERE instrument_id IN (%s) ORDER BY instrument_id
	`, strings.Join(idPlaceholders, ",")), idArgs...)
	if err != nil {
		return nil, fmt.Errorf("list identifiers for instruments: %w", err)
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow)
	for _, r := range results {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &val, &canonical); err != nil {
			return nil, err
		}
		if row := byID[instID.String()]; row != nil {
			row.Identifiers = append(row.Identifiers, db.IdentifierInput{Type: idType, Value: val, Canonical: canonical})
		}
	}
	return results, idRows.Err()
}

// EnsureInstrument implements db.InstrumentDB.
// Finds by any identifier; if not found, creates instrument and inserts identifiers.
// When multiple identifiers resolve to different instruments, merges them eagerly and returns the survivor.
// On unique violation (identifier already exists for another instrument), returns the existing instrument ID (eager merge).
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, exchange, currency, name string, identifiers []db.IdentifierInput, underlyingID string, validFrom, validTo *time.Time) (string, error) {
	if len(identifiers) == 0 {
		return "", fmt.Errorf("at least one identifier required")
	}
	if assetClass != "" && !db.ValidAssetClasses[assetClass] {
		return "", fmt.Errorf("invalid asset_class %q", assetClass)
	}
	if (assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture) && underlyingID == "" {
		return "", fmt.Errorf("underlying_id required when asset_class is %s", assetClass)
	}
	var underlyingUUID *uuid.UUID
	if underlyingID != "" {
		parsed, err := uuid.Parse(underlyingID)
		if err != nil {
			return "", fmt.Errorf("invalid underlying_id: %w", err)
		}
		underlyingUUID = &parsed
	}
	// Look up every identifier and collect distinct instrument IDs (no early return).
	seen := make(map[uuid.UUID]struct{})
	var distinctIDs []uuid.UUID
	for _, idn := range identifiers {
		var existingID uuid.UUID
		err := p.q.QueryRowContext(ctx, `SELECT instrument_id FROM instrument_identifiers WHERE identifier_type = $1 AND value = $2`, idn.Type, idn.Value).Scan(&existingID)
		if err == nil {
			if _, ok := seen[existingID]; !ok {
				seen[existingID] = struct{}{}
				distinctIDs = append(distinctIDs, existingID)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return "", fmt.Errorf("lookup instrument: %w", err)
		}
	}
	// Multiple instruments: merge into one and return survivor.
	if len(distinctIDs) > 1 {
		survivor, err := pickSurvivor(ctx, p.q, distinctIDs)
		if err != nil {
			return "", err
		}
		err = p.runInTx(ctx, func(exec queryable) error {
			for _, id := range distinctIDs {
				if id == survivor {
					continue
				}
				if err := mergeInstruments(ctx, exec, survivor, id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return survivor.String(), nil
	}
	// Exactly one instrument: return it.
	if len(distinctIDs) == 1 {
		return distinctIDs[0].String(), nil
	}
	// None found: create new instrument and add identifiers.
	var newID uuid.UUID
	err := p.runInTx(ctx, func(exec queryable) error {
		err := exec.QueryRowContext(ctx, `
			INSERT INTO instruments (asset_class, exchange, currency, name, underlying_id, valid_from, valid_to)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, nullStr(assetClass), nullStr(exchange), nullStr(currency), nullStr(name), nullUUID(underlyingUUID), nullTime(validFrom), nullTime(validTo)).Scan(&newID)
		if err != nil {
			return err
		}
		for _, idn := range identifiers {
			canonical := idn.Canonical
			_, err = exec.ExecContext(ctx, `INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical) VALUES ($1, $2, $3, $4)`, newID, idn.Type, idn.Value, canonical)
			if err != nil {
				if isUniqueViolation(err) {
					return errIdentifierExists // rollback tx; caller will look up existing id
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errIdentifierExists) {
			for _, idn := range identifiers {
				var existingID uuid.UUID
				rowErr := p.q.QueryRowContext(ctx, `SELECT instrument_id FROM instrument_identifiers WHERE identifier_type = $1 AND value = $2`, idn.Type, idn.Value).Scan(&existingID)
				if rowErr == nil {
					return existingID.String(), nil
				}
			}
		}
		return "", err
	}
	return newID.String(), nil
}

func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}
