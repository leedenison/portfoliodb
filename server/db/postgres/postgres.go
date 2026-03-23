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
	"github.com/jmoiron/sqlx"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// queryable is satisfied by *sqlx.DB and *sqlx.Tx.
type queryable interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error)
	QueryRowxContext(ctx context.Context, query string, args ...interface{}) *sqlx.Row
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

// Postgres implements db.DB using PostgreSQL.
type Postgres struct {
	q queryable
}

// New returns a new Postgres DB implementation.
func New(conn *sqlx.DB) *Postgres {
	return &Postgres{q: conn}
}

// NewWithQueryable returns a Postgres that uses the given queryable (e.g. *sqlx.Tx for tests).
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

// runInTx runs f inside a transaction. When p.q is *sqlx.DB it begins a new tx; when p.q is *sqlx.Tx (e.g. in tests) it runs f on that tx and does not commit.
func (p *Postgres) runInTx(ctx context.Context, f func(exec queryable) error) error {
	switch q := p.q.(type) {
	case *sqlx.Tx:
		return f(q)
	case *sqlx.DB:
		tx, err := q.BeginTxx(ctx, nil)
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

func nullFloat(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// instrumentRow is the sqlx-scannable shape of an instruments row with optional exchange JOIN fields.
type instrumentRow struct {
	ID                  uuid.UUID  `db:"id"`
	AssetClass          *string    `db:"asset_class"`
	ExchangeMIC         *string    `db:"exchange_mic"`
	Currency            *string    `db:"currency"`
	Name                *string    `db:"name"`
	UnderlyingID        *string    `db:"underlying_id"`
	ValidFrom           *time.Time `db:"valid_from"`
	ValidTo             *time.Time `db:"valid_to"`
	CIK                 *string    `db:"cik"`
	SICCode             *string    `db:"sic_code"`
	ExchangeName        *string    `db:"exchange_name"`
	ExchangeAcronym     *string    `db:"exchange_acronym"`
	ExchangeCountryCode *string    `db:"exchange_country_code"`
}

func (r *instrumentRow) toDBRow() *db.InstrumentRow {
	return &db.InstrumentRow{
		ID:                  r.ID.String(),
		AssetClass:          r.AssetClass,
		ExchangeMIC:         r.ExchangeMIC,
		Currency:            r.Currency,
		Name:                r.Name,
		UnderlyingID:        r.UnderlyingID,
		ValidFrom:           r.ValidFrom,
		ValidTo:             r.ValidTo,
		CIK:                 r.CIK,
		SICCode:             r.SICCode,
		ExchangeName:        r.ExchangeName,
		ExchangeAcronym:     r.ExchangeAcronym,
		ExchangeCountryCode: r.ExchangeCountryCode,
	}
}

// holdingRow is the sqlx-scannable shape for computing holdings.
type holdingRow struct {
	Broker   string  `db:"broker"`
	Account  string  `db:"account"`
	InstDesc string  `db:"instrument_description"`
	InstID   *string `db:"instrument_id"`
	Quantity float64 `db:"quantity"`
}

func (r *holdingRow) toProto() *apiv1.Holding {
	h := &apiv1.Holding{
		Broker:                strToBroker(r.Broker),
		InstrumentDescription: r.InstDesc,
		Quantity:              r.Quantity,
		Account:               r.Account,
	}
	if r.InstID != nil {
		h.InstrumentId = *r.InstID
	}
	return h
}

// txRow is the sqlx-scannable shape for transaction rows.
type txRow struct {
	Broker           string   `db:"broker"`
	Account          string   `db:"account"`
	Timestamp        time.Time `db:"timestamp"`
	InstDesc         string   `db:"instrument_description"`
	TxType           string   `db:"tx_type"`
	Quantity         float64  `db:"quantity"`
	TradingCcy       *string  `db:"trading_currency"`
	SettleCcy        *string  `db:"settlement_currency"`
	UnitPrice        *float64 `db:"unit_price"`
	InstID           *string  `db:"instrument_id"`
	SyntheticPurpose *string  `db:"synthetic_purpose"`
}

func (r *txRow) toProto() *apiv1.PortfolioTx {
	tx := &apiv1.Tx{
		Timestamp:             timeToTs(r.Timestamp),
		InstrumentDescription: r.InstDesc,
		Type:                  strToTxType(r.TxType),
		Quantity:              r.Quantity,
		Account:               r.Account,
	}
	if r.TradingCcy != nil {
		tx.TradingCurrency = *r.TradingCcy
	}
	if r.SettleCcy != nil {
		tx.SettlementCurrency = *r.SettleCcy
	}
	if r.UnitPrice != nil {
		tx.UnitPrice = *r.UnitPrice
	}
	if r.InstID != nil {
		tx.InstrumentId = *r.InstID
	}
	if r.SyntheticPurpose != nil {
		tx.SyntheticPurpose = *r.SyntheticPurpose
	}
	return &apiv1.PortfolioTx{
		Broker:  strToBroker(r.Broker),
		Tx:      tx,
		Account: r.Account,
	}
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
