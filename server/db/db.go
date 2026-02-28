package db

//go:generate go run go.uber.org/mock/mockgen -source=db.go -destination=mock/db_mock.go -package=mock

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DB is the database abstraction used by the service layer.
type DB interface {
	UserDB
	PortfolioDB
	TxDB
	HoldingsDB
	JobDB
	InstrumentDB
}

// IdentificationError is stored per job for identification warnings (e.g. broker description only, plugin timeout).
type IdentificationError struct {
	RowIndex               int32
	InstrumentDescription string
	Message                string
}

// UserDB provides user operations.
type UserDB interface {
	GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error)
	GetUserByAuthSub(ctx context.Context, authSub string) (string, error)
}

// PortfolioDB provides portfolio CRUD.
type PortfolioDB interface {
	ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error)
	GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error)
	CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error)
	UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error)
	DeletePortfolio(ctx context.Context, portfolioID string) error
	PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error)
}

// TxDB provides transaction write and list.
type TxDB interface {
	ReplaceTxsInPeriod(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string) error
	UpsertTx(ctx context.Context, portfolioID, broker string, tx *apiv1.Tx, instrumentID string) error
	ListTxs(ctx context.Context, portfolioID string, broker *apiv1.Broker, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
}

// HoldingsDB computes holdings at a point in time.
type HoldingsDB interface {
	ComputeHoldings(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
}

// JobDB provides ingestion job operations.
type JobDB interface {
	CreateJob(ctx context.Context, portfolioID, broker string, periodFrom, periodTo *timestamppb.Timestamp) (string, error)
	GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, []IdentificationError, string, error)
	SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error
	AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error
	AppendIdentificationErrors(ctx context.Context, jobID string, errs []IdentificationError) error
	ListPendingJobIDs(ctx context.Context) ([]string, error)
}

// IdentifierInput is a single (type, value) for EnsureInstrument.
// Canonical is false only for broker-description identifiers; true for standard identifiers (ISIN, CUSIP, etc.).
type IdentifierInput struct {
	Type      string
	Value     string
	Canonical bool // default true when not set for backward compat
}

// PluginConfigRow is one row from identifier_plugin_config for enabled plugins.
type PluginConfigRow struct {
	PluginID   string
	Precedence int
	Config     []byte
}

// InstrumentRow is a single instrument with its identifiers (for API responses).
type InstrumentRow struct {
	ID         string
	AssetClass string
	Exchange   string
	Currency   string
	Name       string
	Identifiers []IdentifierInput
}

// InstrumentDB provides instrument resolution and plugin config.
type InstrumentDB interface {
	// EnsureInstrument finds an instrument by any of the given identifiers, or creates one with the given canonical fields and identifiers. Returns instrument ID. On unique violation (identifier already exists for another instrument), merges and returns the existing instrument ID.
	EnsureInstrument(ctx context.Context, assetClass, exchange, currency, name string, identifiers []IdentifierInput) (string, error)
	// FindInstrumentByBrokerDescription looks up instrument_id by (broker, instrument_description) via instrument_identifiers. Returns "" if not found.
	FindInstrumentByBrokerDescription(ctx context.Context, broker, instrumentDescription string) (string, error)
	// GetInstrument returns an instrument by ID with its identifiers, or nil if not found.
	GetInstrument(ctx context.Context, instrumentID string) (*InstrumentRow, error)
	// ListEnabledPluginConfigs returns enabled plugins ordered by precedence descending (higher first).
	ListEnabledPluginConfigs(ctx context.Context) ([]PluginConfigRow, error)
	// ListInstrumentsForExport returns all instruments that have at least one identifier with canonical = true. If exchangeFilter != "", filter by instruments.exchange. Order by instruments.id.
	ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*InstrumentRow, error)
}
