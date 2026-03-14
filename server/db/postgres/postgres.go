package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
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
	case apiv1.Broker_FIDELITY:
		return "Fidelity", nil
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
	case "Fidelity":
		return apiv1.Broker_FIDELITY
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

func decodePageToken(token string) int64 {
	if token == "" {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}
	offset, _ := strconv.ParseInt(string(b), 10, 64)
	return offset
}

func encodePageToken(offset int64) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}

// inClauseUUIDs builds a SQL IN clause placeholder string and args for a slice of UUIDs, numbered from $1.
func inClauseUUIDs(ids []uuid.UUID) (string, []interface{}) {
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ids[i]
	}
	return strings.Join(placeholders, ","), args
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanInstrumentRow(s scanner) (*db.InstrumentRow, uuid.UUID, error) {
	var row db.InstrumentRow
	var id uuid.UUID
	var assetClass, exchange, currency, name, underlyingID sql.NullString
	var validFrom, validTo sql.NullTime
	if err := s.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
		return nil, uuid.Nil, err
	}
	row.ID = id.String()
	row.AssetClass = strFromNull(assetClass)
	row.Exchange = strFromNull(exchange)
	row.Currency = strFromNull(currency)
	row.Name = strFromNull(name)
	row.UnderlyingID = strFromNull(underlyingID)
	row.ValidFrom = timeFromNull(validFrom)
	row.ValidTo = timeFromNull(validTo)
	return &row, id, nil
}

// loadIdentifiers batch-loads instrument identifiers for the given IDs and attaches them to the corresponding rows.
func loadIdentifiers(ctx context.Context, q queryable, ids []uuid.UUID, rows []*db.InstrumentRow) error {
	if len(ids) == 0 {
		return nil
	}
	inClause, args := inClauseUUIDs(ids)
	idRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, domain, value, canonical
		FROM instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id
	`, inClause), args...)
	if err != nil {
		return err
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var domain sql.NullString
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &domain, &val, &canonical); err != nil {
			return err
		}
		idn := db.IdentifierInput{Type: idType, Value: val, Canonical: canonical}
		if domain.Valid {
			idn.Domain = domain.String
		}
		if r := byID[instID.String()]; r != nil {
			r.Identifiers = append(r.Identifiers, idn)
		}
	}
	return idRows.Err()
}
