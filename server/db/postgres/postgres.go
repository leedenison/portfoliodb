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
	"github.com/lib/pq"
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
	// settler decides which of the postings a write stores are legs of one event.
	// Nil stores them as they arrive, one group each, which is what a fixture that
	// is not about grouping wants. See groupWritten.
	settler db.Settler
}

// New returns a new Postgres DB implementation.
func New(conn *sqlx.DB) *Postgres {
	return &Postgres{q: conn}
}

// NewWithQueryable returns a Postgres that uses the given queryable (e.g. *sqlx.Tx for tests).
func NewWithQueryable(q queryable) *Postgres {
	return &Postgres{q: q}
}

// WithSettler returns the store with a partition engine wired in, for the process
// that has one. Kept off the db.DB interface: what decides a partition is a choice
// made once where the process is assembled, not something a caller passes in.
func (p *Postgres) WithSettler(s db.Settler) *Postgres {
	p.settler = s
	return p
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

// The tx type and account type vocabularies live in the db package, beside the
// broker and asset class ones, so that a caller outside this package -- the
// archive export, which names an account type in a file -- reads them without
// importing a driver.

func txTypeToStr(t typev1.TxType) (string, error) { return db.TxTypeToStr(t) }

func strToTxType(s string) typev1.TxType { return db.StrToTxType(s) }

func accountTypeToStr(a typev1.AccountType) (string, error) { return db.AccountTypeToStr(a) }

func strToAccountType(s string) typev1.AccountType { return db.StrToAccountType(s) }

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
	UnderlyingListingID *string          `db:"underlying_listing_id"`
	UnderlyingID        *string          `db:"underlying_id"`
	ValidFrom           *time.Time       `db:"valid_from"`
	ValidBefore         *time.Time       `db:"valid_before"`
	CIK                 *string          `db:"cik"`
	SICCode             *string          `db:"sic_code"`
	Strike              *decimal.Decimal `db:"strike"`
	Expiry              *time.Time       `db:"expiry"`
	PutCall             *string          `db:"put_call"`
	ContractMultiplier  decimal.Decimal  `db:"contract_multiplier"`
	ExchangeName        *string          `db:"exchange_name"`
	ExchangeAcronym     *string          `db:"exchange_acronym"`
	ExchangeCountryCode *string          `db:"exchange_country_code"`
	// The underlying named the way a file names it, populated by the export
	// query alone. Everything else identifies an underlying by its listing, and
	// by the security that listing belongs to.
	UnderlyingIdentifierType   *string `db:"underlying_identifier_type"`
	UnderlyingIdentifierValue  *string `db:"underlying_identifier_value"`
	UnderlyingIdentifierDomain *string `db:"underlying_identifier_domain"`
	// The currency of the line the contract delivers, which with the identifier
	// above is what names that line in a file.
	UnderlyingCurrency *string `db:"underlying_currency"`
}

func (r *instrumentRow) toDBRow() *db.InstrumentRow {
	return &db.InstrumentRow{
		ID:                  r.ID.String(),
		AssetClass:          r.AssetClass,
		ExchangeMIC:         r.ExchangeMIC,
		Currency:            r.Currency,
		Name:                r.Name,
		Exchange:            r.Exchange,
		UnderlyingListingID: r.UnderlyingListingID,
		UnderlyingID:        r.UnderlyingID,
		ValidFrom:           r.ValidFrom,
		ValidBefore:         r.ValidBefore,
		CIK:                 r.CIK,
		SICCode:             r.SICCode,
		Strike:              r.Strike,
		Expiry:              r.Expiry,
		PutCall:             r.PutCall,
		ContractMultiplier:  r.ContractMultiplier,
		ExchangeName:        r.ExchangeName,
		ExchangeAcronym:     r.ExchangeAcronym,
		ExchangeCountryCode: r.ExchangeCountryCode,

		Underlying:         r.underlyingRef(),
		UnderlyingCurrency: derefStr(r.UnderlyingCurrency),
	}
}

// derefStr reads a nullable text column as the empty string the domain types use
// for absent.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// underlyingRef assembles the underlying's name out of the three nullable
// columns the export query selects. They are null together and populated
// together, which is what the single pointer on db.InstrumentRow says and these
// three columns cannot.
func (r *instrumentRow) underlyingRef() *db.InstrumentRef {
	if r.UnderlyingIdentifierType == nil {
		return nil
	}
	ref := &db.InstrumentRef{Type: *r.UnderlyingIdentifierType}
	if r.UnderlyingIdentifierValue != nil {
		ref.Value = *r.UnderlyingIdentifierValue
	}
	if r.UnderlyingIdentifierDomain != nil {
		ref.Domain = *r.UnderlyingIdentifierDomain
	}
	return ref
}

// holdingRow is the sqlx-scannable shape for computing holdings.
type holdingRow struct {
	Broker      string          `db:"broker"`
	Account     string          `db:"account"`
	InstDesc    string          `db:"instrument_description"`
	InstID      *string         `db:"instrument_id"`
	ListingID   *string         `db:"listing_id"`
	Currency    string          `db:"currency"`
	SplitAdjQty decimal.Decimal `db:"split_adjusted_quantity"`
}

func (r *holdingRow) toProto() *apiv1.Holding {
	h := &apiv1.Holding{
		Broker:                strToBroker(r.Broker),
		InstrumentDescription: r.InstDesc,
		SplitAdjustedQuantity: r.SplitAdjQty.String(),
		Account:               r.Account,
		Currency:              r.Currency,
	}
	if r.InstID != nil {
		h.InstrumentId = *r.InstID
	}
	if r.ListingID != nil {
		h.ListingId = *r.ListingID
	}
	return h
}

// txRow is the sqlx-scannable shape for transaction rows.
type txRow struct {
	Broker            string           `db:"broker"`
	Account           string           `db:"account"`
	OrderDate         time.Time        `db:"order_date"`
	TradeDate         time.Time        `db:"trade_date"`
	InstDesc          string           `db:"instrument_description"`
	BrokerTxTypes     pq.StringArray   `db:"broker_tx_type"`
	ResolvedTxType    string           `db:"resolved_tx_type"`
	AssetClassHint    *string          `db:"asset_class_hint"`
	Quantity          decimal.Decimal  `db:"quantity"`
	SplitAdjQty       decimal.Decimal  `db:"split_adjusted_quantity"`
	TradingCcy        *string          `db:"trading_currency"`
	SettleCcy         *string          `db:"settlement_currency"`
	UnitPrice         *decimal.Decimal `db:"unit_price"`
	SplitAdjUnitPrice *decimal.Decimal `db:"split_adjusted_unit_price"`
	InstID            *string          `db:"instrument_id"`
	SyntheticPurpose  *string          `db:"synthetic_purpose"`
	AccountType       string           `db:"account_type"`
	GroupID           string           `db:"group_id"`
	GroupTimestamp    time.Time        `db:"group_timestamp"`
}

func (r *txRow) toProto() *apiv1.PortfolioTx {
	tx := &apiv1.Tx{
		OrderDate:             timeToTs(r.OrderDate),
		TradeDate:             timeToTs(r.TradeDate),
		InstrumentDescription: r.InstDesc,
		BrokerTxType:          db.StrsToTxTypes(r.BrokerTxTypes),
		ResolvedTxType:        strToTxType(r.ResolvedTxType),
		// String() trims trailing zeros, so a value scanned out of the
		// NUMERIC(38, 12) split-adjusted column arrives in the same form as the
		// raw one it equals when no split applies. That is what lets the client
		// compare the two to decide whether to show an adjustment.
		Quantity:              r.Quantity.String(),
		SplitAdjustedQuantity: decStrPtr(&r.SplitAdjQty),
		Account:               r.Account,
		AccountType:           strToAccountType(r.AccountType),
		GroupId:               r.GroupID,
		GroupTimestamp:        timeToTs(r.GroupTimestamp),
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
	if r.AssetClassHint != nil {
		tx.AssetClassHint = db.StrToAssetClass(*r.AssetClassHint)
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

// loadIdentifiers batch-loads an instrument's security-grain identifiers and
// attaches them to the corresponding rows. What a listing-grain type names is
// one currency line, so those rows come back through loadListings attached to
// the listing they name.
func loadIdentifiers(ctx context.Context, q queryable, ids []uuid.UUID, rows []*db.InstrumentRow) error {
	if len(ids) == 0 {
		return nil
	}
	inClause, args := inClauseUUIDs(ids)
	idRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before
		FROM instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id, valid_before IS NULL DESC, valid_before DESC
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
		var validFrom, validBefore sql.NullTime
		if err := idRows.Scan(&instID, &idType, &domain, &val, &canonical, &validFrom, &validBefore); err != nil {
			return err
		}
		idn := db.IdentifierInput{
			Ref:       db.InstrumentRef{Type: idType, Value: val},
			Canonical: canonical,
		}
		if domain.Valid {
			idn.Ref.Domain = domain.String
		}
		if validFrom.Valid {
			idn.ValidFrom = &validFrom.Time
		}
		if validBefore.Valid {
			idn.ValidBefore = &validBefore.Time
		}
		if r := byID[instID.String()]; r != nil {
			r.Identifiers = append(r.Identifiers, idn)
		}
	}
	return idRows.Err()
}

// loadProviderIdentifiers batch-loads security-grain provider identifiers, and
// leaves the listing-grain ones to loadListings for the reason above.
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
