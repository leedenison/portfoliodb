package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
func (p *Postgres) GetUserByAuthSub(ctx context.Context, authSub string) (string, error) {
	var id uuid.UUID
	err := p.q.QueryRowContext(ctx, `SELECT id FROM users WHERE auth_sub = $1`, authSub).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get user by auth sub: %w", err)
	}
	return id.String(), nil
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
func (p *Postgres) ReplaceTxsInPeriod(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
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
		for _, t := range txs {
			ts, err := tsToTime(t.Timestamp)
			if err != nil {
				return err
			}
			txTypeStr, err := txTypeToStr(t.Type)
			if err != nil {
				return err
			}
			_, err = exec.ExecContext(ctx, `
				INSERT INTO txs (portfolio_id, broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, portUUID, broker, ts, t.InstrumentDescription, txTypeStr, t.Quantity, nullStr(t.Currency), nullFloat(t.UnitPrice))
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

func nullFloat(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

// UpsertTx implements db.TxDB.
func (p *Postgres) UpsertTx(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx) error {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return fmt.Errorf("invalid portfolio id: %w", err)
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
		INSERT INTO txs (portfolio_id, broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (portfolio_id, broker, timestamp, instrument_description)
		DO UPDATE SET tx_type = $5, quantity = $6, currency = $7, unit_price = $8
	`, portUUID, broker, ts, tx.InstrumentDescription, txTypeStr, tx.Quantity, nullStr(tx.Currency), nullFloat(tx.UnitPrice))
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
		SELECT broker, timestamp, instrument_description, tx_type, quantity, currency, unit_price
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
		if err := rows.Scan(&brokerStr, &ts, &instDesc, &txTypeStr, &qty, &currency, &unitPrice); err != nil {
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
		SELECT broker, instrument_description,
			SUM(CASE WHEN tx_type LIKE 'SELL%' THEN -quantity ELSE quantity END) AS quantity
		FROM txs
		WHERE portfolio_id = $1 AND timestamp <= $2
		GROUP BY portfolio_id, broker, instrument_description
		HAVING SUM(CASE WHEN tx_type LIKE 'SELL%' THEN -quantity ELSE quantity END) != 0
	`, portUUID, asOfT)
	if err != nil {
		return nil, nil, fmt.Errorf("compute holdings: %w", err)
	}
	defer rows.Close()
	var out []*apiv1.Holding
	for rows.Next() {
		var brokerStr string
		var instDesc string
		var qty float64
		if err := rows.Scan(&brokerStr, &instDesc, &qty); err != nil {
			return nil, nil, err
		}
		out = append(out, &apiv1.Holding{
			Broker:                strToBroker(brokerStr),
			InstrumentDescription: instDesc,
			Quantity:              qty,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, timeToTs(asOfT), nil
}

// CreateJob implements db.JobDB.
func (p *Postgres) CreateJob(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp) (string, error) {
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
		INSERT INTO ingestion_jobs (portfolio_id, broker, period_from, period_to, status)
		VALUES ($1, $2, $3, $4, 'PENDING')
		RETURNING id
	`, portUUID, broker, fromT, toT).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	return id.String(), nil
}

// GetJob implements db.JobDB.
func (p *Postgres) GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, string, error) {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", fmt.Errorf("invalid job id: %w", err)
	}
	var statusStr string
	var portfolioID uuid.UUID
	err = p.q.QueryRowContext(ctx, `SELECT status, portfolio_id FROM ingestion_jobs WHERE id = $1`, jobUUID).Scan(&statusStr, &portfolioID)
	if err == sql.ErrNoRows {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", nil
	}
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", fmt.Errorf("get job: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `SELECT row_index, field, message FROM validation_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", fmt.Errorf("get validation errors: %w", err)
	}
	defer rows.Close()
	var errs []*apiv1.ValidationError
	for rows.Next() {
		var rowIndex int32
		var field, message string
		if err := rows.Scan(&rowIndex, &field, &message); err != nil {
			return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", err
		}
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: field, Message: message})
	}
	if err := rows.Err(); err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, "", err
	}
	return strToJobStatus(statusStr), errs, portfolioID.String(), nil
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
