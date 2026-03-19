package db

//go:generate go run go.uber.org/mock/mockgen -source=db.go -destination=mock/db_mock.go -package=mock

import (
	"context"
	"time"

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
	DescriptionPluginDB
	PricePluginDB
	PriceCacheDB
	PriceFetchBlockDB
	EODPriceListDB
}

// PriceFetchBlockDB manages permanently blocked (instrument, plugin) pairs.
type PriceFetchBlockDB interface {
	ListPriceFetchBlocks(ctx context.Context) ([]PriceFetchBlock, error)
	// BlockedPluginsForInstruments returns blocked plugin IDs keyed by instrument ID.
	BlockedPluginsForInstruments(ctx context.Context, instrumentIDs []string) (map[string]map[string]bool, error)
	CreatePriceFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error
	DeletePriceFetchBlock(ctx context.Context, instrumentID, pluginID string) error
}

// DateRange is a half-open [From, To) date range. Both values are midnight UTC.
type DateRange struct {
	From time.Time // inclusive
	To   time.Time // exclusive
}

// InstrumentDateRanges groups date ranges by instrument.
type InstrumentDateRanges struct {
	InstrumentID string
	Ranges       []DateRange
}

// HeldRangesOpts controls holdings range calculation.
type HeldRangesOpts struct {
	ExtendToToday bool // extend open positions to today
	LookbackDays  int  // extend held_from backwards by N calendar days
}

// EODPrice is a single end-of-day price row for UpsertPrices.
type EODPrice struct {
	InstrumentID string
	PriceDate    time.Time
	Open         *float64
	High         *float64
	Low          *float64
	Close        float64
	Volume       *int64
	DataProvider string
}

// PriceCacheDB provides price cache management.
type PriceCacheDB interface {
	// HeldRanges computes system-wide date ranges during which any user held
	// a non-zero position in each identified instrument.
	HeldRanges(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// PriceCoverage returns contiguous date ranges for which eod_prices has data.
	// If instrumentIDs is non-empty, only those instruments are returned.
	PriceCoverage(ctx context.Context, instrumentIDs []string) ([]InstrumentDateRanges, error)
	// PriceGaps computes needed ranges minus cached ranges per instrument.
	PriceGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// UpsertPrices inserts or updates EOD prices. On conflict (instrument_id, price_date)
	// the existing row is overwritten with the new values.
	UpsertPrices(ctx context.Context, prices []EODPrice) error
}

// PricePluginDB provides price plugin config management.
type PricePluginDB interface {
	// ListEnabledPricePluginConfigs returns enabled price plugins ordered by precedence descending.
	ListEnabledPricePluginConfigs(ctx context.Context) ([]PluginConfigRow, error)
	// ListPricePluginConfigs returns all price plugin config rows (for admin UI). Order by precedence descending.
	ListPricePluginConfigs(ctx context.Context) ([]PluginConfigRowFull, error)
	// GetPricePluginConfig returns the config row for pluginID. Returns (nil, sql.ErrNoRows) when no row exists.
	GetPricePluginConfig(ctx context.Context, pluginID string) (*PluginConfigRowFull, error)
	// InsertPricePluginConfig creates a new price plugin config row.
	InsertPricePluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte, maxHistoryDays *int) (*PluginConfigRowFull, error)
	// UpdatePricePluginConfig updates enabled, precedence, config, and/or max_history_days for a price plugin.
	// For maxHistoryDays: nil = no change, pointer to 0 = clear (NULL), pointer to N = set.
	UpdatePricePluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte, maxHistoryDays *int) (*PluginConfigRowFull, error)
}

// DescriptionPluginDB provides description plugin config (extract identifier hints from broker descriptions).
type DescriptionPluginDB interface {
	// ListEnabledDescriptionPluginConfigs returns enabled description plugins ordered by precedence descending.
	ListEnabledDescriptionPluginConfigs(ctx context.Context) ([]PluginConfigRow, error)
	// ListDescriptionPluginConfigs returns all description plugin config rows (for admin UI). Order by precedence descending.
	ListDescriptionPluginConfigs(ctx context.Context) ([]PluginConfigRowFull, error)
	// GetDescriptionPluginConfig returns the config row for pluginID. Returns (nil, sql.ErrNoRows) when no row exists.
	GetDescriptionPluginConfig(ctx context.Context, pluginID string) (*PluginConfigRowFull, error)
	// InsertDescriptionPluginConfig creates a new description plugin config row.
	InsertDescriptionPluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte) (*PluginConfigRowFull, error)
	// UpdateDescriptionPluginConfig updates enabled, precedence, and/or config for a description plugin.
	UpdateDescriptionPluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte) (*PluginConfigRowFull, error)
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
	GetUserByAuthSub(ctx context.Context, authSub string) (userID, role string, err error)
	// GetUserByEmail returns the first user (if any) with the given email (case-insensitive).
	GetUserByEmail(ctx context.Context, email string) (userID string, err error)
	// UpdateUserAuthSub sets auth_sub for the user (e.g. bind Google sub to existing user found by email).
	UpdateUserAuthSub(ctx context.Context, userID, authSub string) error
}

// PortfolioFilter is one filter row for a portfolio view.
type PortfolioFilter struct {
	FilterType  string // "broker", "account", "instrument"
	FilterValue string
}

// PortfolioDB provides portfolio CRUD and filter management.
type PortfolioDB interface {
	ListPortfolios(ctx context.Context, userID string, pageSize int32, pageToken string) ([]*apiv1.Portfolio, string, error)
	GetPortfolio(ctx context.Context, portfolioID string) (*apiv1.Portfolio, string, error)
	CreatePortfolio(ctx context.Context, userID, name string) (*apiv1.Portfolio, error)
	UpdatePortfolio(ctx context.Context, portfolioID, name string) (*apiv1.Portfolio, error)
	DeletePortfolio(ctx context.Context, portfolioID string) error
	PortfolioBelongsToUser(ctx context.Context, portfolioID, userID string) (bool, error)
	ListPortfolioFilters(ctx context.Context, portfolioID string) ([]PortfolioFilter, error)
	SetPortfolioFilters(ctx context.Context, portfolioID string, filters []PortfolioFilter) error
}

// TxDB provides transaction write and list.
type TxDB interface {
	ReplaceTxsInPeriod(ctx context.Context, userID, broker string, periodFrom, periodTo *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string) error
	CreateTx(ctx context.Context, userID, broker, account string, tx *apiv1.Tx, instrumentID string) error
	ListTxs(ctx context.Context, userID string, broker *apiv1.Broker, account string, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
	ListTxsByPortfolio(ctx context.Context, portfolioID string, periodFrom, periodTo *timestamppb.Timestamp, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
}

// HoldingsDB computes holdings at a point in time.
type HoldingsDB interface {
	ComputeHoldings(ctx context.Context, userID string, broker *apiv1.Broker, account string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
	ComputeHoldingsForPortfolio(ctx context.Context, portfolioID string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
}

// JobRow is a job summary for list views.
type JobRow struct {
	ID                       string
	Filename                 string
	Broker                   string
	Status                   string
	CreatedAt                time.Time
	ValidationErrorCount     int32
	IdentificationErrorCount int32
}

// JobDB provides ingestion job operations.
type JobDB interface {
	CreateJob(ctx context.Context, userID, broker, source, filename string, periodFrom, periodTo *timestamppb.Timestamp) (string, error)
	GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, []IdentificationError, string, int32, int32, error) // returns (status, validationErrors, idErrors, userID, totalCount, processedCount, error)
	SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error
	SetJobTotalCount(ctx context.Context, jobID string, total int32) error
	IncrJobProcessedCount(ctx context.Context, jobID string) error
	AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error
	AppendIdentificationErrors(ctx context.Context, jobID string, errs []IdentificationError) error
	ListPendingJobIDs(ctx context.Context) ([]string, error)
	// ListJobs returns jobs for a user, newest first, with error counts. Returns (rows, totalCount, nextPageToken, error).
	ListJobs(ctx context.Context, userID string, pageSize int32, pageToken string) ([]JobRow, int32, string, error)
}

// IdentifierInput is a single (type, domain, value) for EnsureInstrument.
// Domain is empty or nil for broker-description and for identifiers that have no domain (e.g. ISIN, CUSIP).
// Canonical is false only for broker-description identifiers; true for standard identifiers (ISIN, CUSIP, etc.).
type IdentifierInput struct {
	Type      string
	Domain    string // empty or NULL for no domain
	Value     string
	Canonical bool   // default true when not set for backward compat
}

// PluginConfigRow is one row from identifier_plugin_config for enabled plugins.
// MaxHistoryDays is only populated for price plugins; nil for identifier/description plugins.
type PluginConfigRow struct {
	PluginID       string
	Precedence     int
	Config         []byte
	MaxHistoryDays *int
}

// PluginConfigRowFull is a full row from identifier_plugin_config (includes enabled). Used for admin list/update.
// MaxHistoryDays is only populated for price plugins; nil for identifier/description plugins.
type PluginConfigRowFull struct {
	PluginID       string
	Enabled        bool
	Precedence     int
	Config         []byte
	MaxHistoryDays *int
}

// PriceFetchBlock records a permanently blocked (instrument, plugin) pair.
type PriceFetchBlock struct {
	InstrumentID string
	PluginID     string
	Reason       string
	CreatedAt    time.Time
}

// EODPriceRow is a single end-of-day price row for the admin price list.
type EODPriceRow struct {
	InstrumentID          string
	InstrumentDisplayName string
	PriceDate             time.Time
	Open                  *float64
	High                  *float64
	Low                   *float64
	Close                 float64
	AdjustedClose         *float64
	Volume                *int64
	DataProvider          string
	FetchedAt             time.Time
}

// EODPriceListDB provides paginated listing of EOD prices for admin UI.
type EODPriceListDB interface {
	// ListPrices returns EOD prices with optional search, date range, and provider filters.
	// Returns (rows, totalCount, nextPageToken, error).
	ListPrices(ctx context.Context, search string, dateFrom, dateTo time.Time,
		dataProvider string, pageSize int32, pageToken string) ([]EODPriceRow, int32, string, error)
}

// Valid asset class values (controlled vocabulary).
const (
	AssetClassStock       = "STOCK"
	AssetClassETF         = "ETF"
	AssetClassFixedIncome = "FIXED_INCOME"
	AssetClassMutualFund  = "MUTUAL_FUND"
	AssetClassOption      = "OPTION"
	AssetClassFuture      = "FUTURE"
	AssetClassCash        = "CASH"
	AssetClassUnknown     = "UNKNOWN"
)

// ValidAssetClasses is the set of allowed asset_class values for validation.
var ValidAssetClasses = map[string]bool{
	AssetClassStock: true, AssetClassETF: true, AssetClassFixedIncome: true, AssetClassMutualFund: true,
	AssetClassOption: true, AssetClassFuture: true, AssetClassCash: true, AssetClassUnknown: true,
}

// InstrumentRow is a single instrument with its identifiers (for API responses).
type InstrumentRow struct {
	ID           string
	AssetClass   string
	Exchange     string
	Currency     string
	Name         string
	UnderlyingID string
	ValidFrom    *time.Time
	ValidTo      *time.Time
	Identifiers  []IdentifierInput
}

// InstrumentDB provides instrument resolution and plugin config.
type InstrumentDB interface {
	// EnsureInstrument finds an instrument by any of the given identifiers, or creates one with the given canonical fields and identifiers. Returns instrument ID. On unique violation (identifier already exists for another instrument), merges and returns the existing instrument ID. When assetClass is OPTION or FUTURE, underlyingID must be non-empty.
	EnsureInstrument(ctx context.Context, assetClass, exchange, currency, name string, identifiers []IdentifierInput, underlyingID string, validFrom, validTo *time.Time) (string, error)
	// FindInstrumentByIdentifier looks up instrument_id by (identifier_type, domain, value). Returns "" if not found. Use empty domain for no domain.
	FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error)
	// FindInstrumentByTypeAndValue looks up instrument_id by (identifier_type, value) with any domain. Returns "" if not found or if multiple instruments match (ambiguous).
	FindInstrumentByTypeAndValue(ctx context.Context, identifierType, value string) (string, error)
	// FindInstrumentBySourceDescription looks up instrument_id by (source, NULL domain, instrument_description). Returns "" if not found.
	FindInstrumentBySourceDescription(ctx context.Context, source, description string) (string, error)
	// GetInstrument returns an instrument by ID with its identifiers, or nil if not found.
	GetInstrument(ctx context.Context, instrumentID string) (*InstrumentRow, error)
	// ListInstrumentsByIDs returns instruments by ID slice (for batch underlying lookup). Missing IDs are omitted; order not guaranteed.
	ListInstrumentsByIDs(ctx context.Context, ids []string) ([]*InstrumentRow, error)
	// ListEnabledPluginConfigs returns enabled plugins ordered by precedence descending (higher first).
	ListEnabledPluginConfigs(ctx context.Context) ([]PluginConfigRow, error)
	// ListPluginConfigs returns all plugin config rows (for admin UI). Order by precedence descending.
	ListPluginConfigs(ctx context.Context) ([]PluginConfigRowFull, error)
	// GetPluginConfig returns the config row for pluginID. Returns (nil, sql.ErrNoRows) when no row exists.
	GetPluginConfig(ctx context.Context, pluginID string) (*PluginConfigRowFull, error)
	// InsertPluginConfig creates a new plugin config row. Used by the server on startup when a plugin has no row (from plugin.DefaultConfig()).
	InsertPluginConfig(ctx context.Context, pluginID string, enabled bool, precedence int, config []byte) (*PluginConfigRowFull, error)
	// UpdatePluginConfig updates enabled, precedence, and/or config for a plugin. Zero-value fields in the struct mean "no change" except Config: nil means no change, empty slice means clear config.
	UpdatePluginConfig(ctx context.Context, pluginID string, enabled *bool, precedence *int, config []byte) (*PluginConfigRowFull, error)
	// ListInstrumentsForExport returns all instruments that have at least one identifier with canonical = true. If exchangeFilter != "", filter by instruments.exchange. Order by instruments.id.
	ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*InstrumentRow, error)
	// ListInstruments returns instruments sorted alphabetically by display name (ticker, then name, then broker description). If search is non-empty, only instruments with at least one identifier value matching (case-insensitive substring) are returned. If assetClasses is non-empty, only instruments with matching asset_class are returned. Returns (rows, totalCount, nextPageToken, error).
	ListInstruments(ctx context.Context, search string, assetClasses []string, pageSize int32, pageToken string) ([]*InstrumentRow, int32, string, error)
}
