package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strconv"
	"strings"
	"time"
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

// brokerToStr returns the stored form of a broker: its enum name. Derived rather
// than mapped by hand so a new broker cannot be stored under a spelling that
// strToBroker does not recognise.
func brokerToStr(b typev1.Broker) (string, error) {
	if b == typev1.Broker_BROKER_UNSPECIFIED {
		return "", fmt.Errorf("broker unspecified")
	}
	s, ok := typev1.Broker_name[int32(b)]
	if !ok {
		return "", fmt.Errorf("unknown broker: %v", b)
	}
	return s, nil
}

func strToBroker(s string) typev1.Broker {
	v, ok := typev1.Broker_value[s]
	if !ok {
		return typev1.Broker_BROKER_UNSPECIFIED
	}
	return typev1.Broker(v)
}

func txTypeToStr(t typev1.TxType) (string, error) {
	if t == typev1.TxType_TX_TYPE_UNSPECIFIED {
		return "", fmt.Errorf("tx type unspecified")
	}
	s := t.String()
	if s == "TX_TYPE_UNSPECIFIED" {
		return "", fmt.Errorf("tx type unspecified")
	}
	return s, nil
}

func strToTxType(s string) typev1.TxType {
	v, ok := typev1.TxType_value[s]
	if !ok {
		return typev1.TxType_TX_TYPE_UNSPECIFIED
	}
	return typev1.TxType(v)
}

// accountTypePrefix is stripped from the enum name to get the stored form. The proto
// values are prefixed because enum values share package scope and TxType already
// defines INCOME and TRANSFER; the column stores the bare vocabulary the CHECK
// constraint and the specs use.
const accountTypePrefix = "ACCOUNT_TYPE_"

// accountTypeToStr returns the stored form of an account type: its enum name without
// the prefix. Unspecified is USER rather than an error -- an upload that says nothing
// about a posting's kind is an ordinary broker account posting, which is what almost
// every row is. Derived from the generated names rather than mapped by hand so a new
// type cannot be stored under a spelling that strToAccountType does not recognise.
func accountTypeToStr(a typev1.AccountType) (string, error) {
	if a == typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED {
		return "USER", nil
	}
	s, ok := typev1.AccountType_name[int32(a)]
	if !ok {
		return "", fmt.Errorf("unknown account type: %v", a)
	}
	return strings.TrimPrefix(s, accountTypePrefix), nil
}

func strToAccountType(s string) typev1.AccountType {
	v, ok := typev1.AccountType_value[accountTypePrefix+s]
	if !ok {
		return typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED
	}
	return typev1.AccountType(v)
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
		defer func() { _ = tx.Rollback() }()
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

// parseNullUUID parses an optional UUID column value, treating the empty string as
// NULL rather than as a parse error.
func parseNullUUID(s string) (interface{}, error) {
	if s == "" {
		return nil, nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func nullTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

// nullDecimal maps an absent value to SQL NULL, keying off presence rather than
// zero. A price of zero is a real price -- an option that expires worthless --
// and must survive the round trip distinctly from no price at all.
func nullDecimal(d *decimal.Decimal) interface{} {
	if d == nil {
		return nil
	}
	return *d
}

// parseOptDecimal parses an optional decimal wire field, preserving absence. An
// unset field is nil; so is an empty string, because proto3 implicit presence
// cannot tell "not supplied" from "" on the fields this shares a format with.
func parseOptDecimal(s *string) (*decimal.Decimal, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// decStrPtr renders an optional decimal for the wire, preserving absence.
func decStrPtr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
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
	ID                  uuid.UUID        `db:"id"`
	AssetClass          *string          `db:"asset_class"`
	ExchangeMIC         *string          `db:"exchange_mic"`
	Currency            *string          `db:"currency"`
	Name                *string          `db:"name"`
	Exchange            string           `db:"exchange"`
	UnderlyingID        *string          `db:"underlying_id"`
	ValidFrom           *time.Time       `db:"valid_from"`
	ValidBefore         *time.Time       `db:"valid_before"`
	CIK                 *string          `db:"cik"`
	SICCode             *string          `db:"sic_code"`
	Strike              *decimal.Decimal `db:"strike"`
	Expiry              *time.Time       `db:"expiry"`
	PutCall             *string          `db:"put_call"`
	ContractMultiplier  decimal.Decimal  `db:"contract_multiplier"`
	IdentityAsOf        *time.Time       `db:"identity_as_of"`
	ExchangeName        *string          `db:"exchange_name"`
	ExchangeAcronym     *string          `db:"exchange_acronym"`
	ExchangeCountryCode *string          `db:"exchange_country_code"`
	// The underlying named the way a file names it, populated by the export
	// query alone. Everything else identifies an underlying by UnderlyingID.
	UnderlyingIdentifierType   *string `db:"underlying_identifier_type"`
	UnderlyingIdentifierValue  *string `db:"underlying_identifier_value"`
	UnderlyingIdentifierDomain *string `db:"underlying_identifier_domain"`
}

func (r *instrumentRow) toDBRow() *db.InstrumentRow {
	return &db.InstrumentRow{
		ID:                  r.ID.String(),
		AssetClass:          r.AssetClass,
		ExchangeMIC:         r.ExchangeMIC,
		Currency:            r.Currency,
		Name:                r.Name,
		Exchange:            r.Exchange,
		UnderlyingID:        r.UnderlyingID,
		ValidFrom:           r.ValidFrom,
		ValidBefore:         r.ValidBefore,
		CIK:                 r.CIK,
		SICCode:             r.SICCode,
		Strike:              r.Strike,
		Expiry:              r.Expiry,
		PutCall:             r.PutCall,
		ContractMultiplier:  r.ContractMultiplier,
		IdentityAsOf:        r.IdentityAsOf,
		ExchangeName:        r.ExchangeName,
		ExchangeAcronym:     r.ExchangeAcronym,
		ExchangeCountryCode: r.ExchangeCountryCode,

		UnderlyingIdentifierType:   r.UnderlyingIdentifierType,
		UnderlyingIdentifierValue:  r.UnderlyingIdentifierValue,
		UnderlyingIdentifierDomain: r.UnderlyingIdentifierDomain,
	}
}

// holdingRow is the sqlx-scannable shape for computing holdings.
type holdingRow struct {
	Broker      string          `db:"broker"`
	Account     string          `db:"account"`
	InstDesc    string          `db:"instrument_description"`
	InstID      *string         `db:"instrument_id"`
	SplitAdjQty decimal.Decimal `db:"split_adjusted_quantity"`
}

func (r *holdingRow) toProto() *apiv1.Holding {
	h := &apiv1.Holding{
		Broker:                strToBroker(r.Broker),
		InstrumentDescription: r.InstDesc,
		SplitAdjustedQuantity: r.SplitAdjQty.String(),
		Account:               r.Account,
	}
	if r.InstID != nil {
		h.InstrumentId = *r.InstID
	}
	return h
}

// txRow is the sqlx-scannable shape for transaction rows.
type txRow struct {
	Broker            string           `db:"broker"`
	Account           string           `db:"account"`
	Timestamp         time.Time        `db:"timestamp"`
	InstDesc          string           `db:"instrument_description"`
	TxType            string           `db:"tx_type"`
	Quantity          decimal.Decimal  `db:"quantity"`
	SplitAdjQty       decimal.Decimal  `db:"split_adjusted_quantity"`
	TradingCcy        *string          `db:"trading_currency"`
	SettleCcy         *string          `db:"settlement_currency"`
	UnitPrice         *decimal.Decimal `db:"unit_price"`
	SplitAdjUnitPrice *decimal.Decimal `db:"split_adjusted_unit_price"`
	InstID            *string          `db:"instrument_id"`
	SyntheticPurpose  *string          `db:"synthetic_purpose"`
	AccountType       string           `db:"account_type"`
}

func (r *txRow) toProto() *apiv1.PortfolioTx {
	tx := &apiv1.Tx{
		Timestamp:             timeToTs(r.Timestamp),
		InstrumentDescription: r.InstDesc,
		Type:                  strToTxType(r.TxType),
		// String() trims trailing zeros, so a value scanned out of the
		// NUMERIC(38, 12) split-adjusted column arrives in the same form as the
		// raw one it equals when no split applies. That is what lets the client
		// compare the two to decide whether to show an adjustment.
		Quantity:              r.Quantity.String(),
		SplitAdjustedQuantity: decStrPtr(&r.SplitAdjQty),
		Account:               r.Account,
		AccountType:           strToAccountType(r.AccountType),
	}
	if r.TradingCcy != nil {
		tx.TradingCurrency = *r.TradingCcy
	}
	if r.SettleCcy != nil {
		tx.SettlementCurrency = *r.SettleCcy
	}
	// Carried as pointers so a stored price of zero stays distinct from no price.
	tx.UnitPrice = decStrPtr(r.UnitPrice)
	tx.SplitAdjustedUnitPrice = decStrPtr(r.SplitAdjUnitPrice)
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

// loadProviderIdentifiers batch-loads provider-specific identifiers for the given instrument IDs.
func loadProviderIdentifiers(ctx context.Context, q queryable, ids []uuid.UUID, rows []*db.InstrumentRow) error {
	if len(ids) == 0 {
		return nil
	}
	inClause, args := inClauseUUIDs(ids)
	piRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, provider, identifier_type, domain, value
		FROM provider_instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id
	`, inClause), args...)
	if err != nil {
		return err
	}
	defer piRows.Close()
	byID := make(map[string]*db.InstrumentRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	for piRows.Next() {
		var instID uuid.UUID
		var pi db.ProviderIdentifierInput
		var domain sql.NullString
		if err := piRows.Scan(&instID, &pi.Provider, &pi.Type, &domain, &pi.Value); err != nil {
			return err
		}
		if domain.Valid {
			pi.Domain = domain.String
		}
		if r := byID[instID.String()]; r != nil {
			r.ProviderIdentifiers = append(r.ProviderIdentifiers, pi)
		}
	}
	return piRows.Err()
}
