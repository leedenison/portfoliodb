package postgres

import (
	"context"
	"database/sql"
	"fmt"
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
