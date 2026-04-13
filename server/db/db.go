package db

//go:generate go run go.uber.org/mock/mockgen -source=db.go -destination=mock/db_mock.go -package=mock

import (
	"context"
	"strings"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Plugin category constants.
const (
	PluginCategoryIdentifier     = "identifier"
	PluginCategoryDescription    = "description"
	PluginCategoryPrice          = "price"
	PluginCategoryInflation      = "inflation"
	PluginCategoryCorporateEvent = "corporate_event"
)

// Data provider sentinels for corporate events. Plugin-sourced events use the
// plugin id directly (e.g. "massive", "eodhd"). These sentinels distinguish
// non-plugin sources.
const (
	CorporateEventProviderImport = "import"
	CorporateEventProviderBroker = "broker"
)

// Job type constants for the ingestion_jobs table.
const (
	JobTypeTx             = "tx"
	JobTypePrice          = "price"
	JobTypeCorporateEvent = "corporate_event"
)

// DB is the database abstraction used by the service layer.
type DB interface {
	UserDB
	ServiceAccountDB
	PortfolioDB
	TxDB
	HoldingsDB
	ValuationDB
	JobDB
	InstrumentDB
	PluginConfigDB
	PriceCacheDB
	PriceFetchBlockDB
	EODPriceListDB
	HoldingDeclarationDB
	IgnoredAssetClassDB
	InflationIndexDB
	CorporateEventDB
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
	Synthetic    bool       // true for forward-filled non-trading day prices
	FetchedAt    *time.Time // when the price data was current; nil defaults to now()
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
	// FXGaps computes date ranges where FX rates are needed (non-USD instruments
	// are held) but not yet cached. Returns gaps keyed by FX pair instrument ID.
	FXGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// UpsertPrices inserts or updates EOD prices. On conflict (instrument_id, price_date)
	// real prices always overwrite; synthetic prices only insert when no row exists
	// or the existing row is also synthetic.
	UpsertPrices(ctx context.Context, prices []EODPrice) error
	// UpsertPricesWithFill inserts real bars and generates synthetic LOCF prices
	// for every date in [from, to) that has no real bar, all in a single SQL
	// statement. The last non-synthetic close before `from` seeds the forward-fill.
	UpsertPricesWithFill(ctx context.Context, instrumentID, provider string, bars []EODPrice, from, to time.Time, fetchedAt *time.Time) error
}

// PluginConfigDB provides unified plugin config CRUD for all categories.
type PluginConfigDB interface {
	// ListEnabledPluginConfigs returns enabled plugins for the given category, ordered by precedence descending.
	ListEnabledPluginConfigs(ctx context.Context, category string) ([]PluginConfigRow, error)
	// ListPluginConfigs returns all plugin config rows for the given category (for admin UI). Order by precedence descending.
	ListPluginConfigs(ctx context.Context, category string) ([]PluginConfigRowFull, error)
	// GetPluginConfig returns the config row for (category, pluginID). Returns (nil, sql.ErrNoRows) when no row exists.
	GetPluginConfig(ctx context.Context, category, pluginID string) (*PluginConfigRowFull, error)
	// InsertPluginConfig creates a new plugin config row.
	InsertPluginConfig(ctx context.Context, category, pluginID string, enabled bool, precedence int, config []byte, maxHistoryDays *int) (*PluginConfigRowFull, error)
	// UpdatePluginConfig updates enabled, precedence, config, and/or max_history_days for a plugin.
	// For maxHistoryDays: nil = no change, pointer to 0 = clear (NULL), pointer to N = set.
	UpdatePluginConfig(ctx context.Context, category, pluginID string, enabled *bool, precedence *int, config []byte, maxHistoryDays *int) (*PluginConfigRowFull, error)
	// ReorderPluginConfigs sets precedence for all plugins in a category.
	// pluginIDs is ordered from highest to lowest precedence. All existing
	// plugin IDs for the category must be present.
	ReorderPluginConfigs(ctx context.Context, category string, pluginIDs []string) error
}

// IdentificationError is stored per job for identification warnings (e.g. broker description only, plugin timeout).
type IdentificationError struct {
	RowIndex               int32
	InstrumentDescription string
	Message                string
}

// ServiceAccountRow is a service account returned from the DB.
type ServiceAccountRow struct {
	ID               string
	Name             string
	ClientSecretHash string
	Role             string
}

// ServiceAccountDB provides service account read operations.
type ServiceAccountDB interface {
	// GetServiceAccount returns the service account by ID, or nil if not found.
	GetServiceAccount(ctx context.Context, id string) (*ServiceAccountRow, error)
}

// UserDB provides user operations.
type UserDB interface {
	GetOrCreateUser(ctx context.Context, authSub, name, email string) (string, error)
	GetUserByAuthSub(ctx context.Context, authSub string) (userID, role string, err error)
	// GetUserByEmail returns the first user (if any) with the given email (case-insensitive).
	GetUserByEmail(ctx context.Context, email string) (userID string, err error)
	// UpdateUserAuthSub sets auth_sub for the user (e.g. bind Google sub to existing user found by email).
	UpdateUserAuthSub(ctx context.Context, userID, authSub string) error
	// GetDisplayCurrency returns the user's display currency (ISO 4217).
	GetDisplayCurrency(ctx context.Context, userID string) (string, error)
	// SetDisplayCurrency updates the user's display currency preference.
	SetDisplayCurrency(ctx context.Context, userID, currency string) error
}

// PortfolioFilter is one filter row for a portfolio view.
type PortfolioFilter struct {
	FilterType  string // "broker", "account", "instrument"
	FilterValue string
}

// BrokerAccount is a distinct (broker, account) pair from user transactions.
type BrokerAccount struct {
	Broker  string
	Account string
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
	ListBrokersAndAccounts(ctx context.Context, userID string) ([]BrokerAccount, error)
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

// ValuationPoint is one day's portfolio value.
type ValuationPoint struct {
	Date                time.Time
	TotalValue          float64
	UnpricedInstruments []string
}

// ValuationDB computes daily portfolio values over a date range.
// displayCurrency is an ISO 4217 code (e.g. "USD"). When empty, the caller
// should resolve it from the user's stored preference before calling.
type ValuationDB interface {
	GetPortfolioValuation(ctx context.Context, portfolioID string, dateFrom, dateTo time.Time, displayCurrency string) ([]ValuationPoint, error)
	GetUserValuation(ctx context.Context, userID string, dateFrom, dateTo time.Time, displayCurrency string) ([]ValuationPoint, error)
}

// JobRow is a job summary for list views.
type JobRow struct {
	ID                       string
	JobType                  string
	Filename                 string
	Broker                   string
	Status                   string
	CreatedAt                time.Time
	ValidationErrorCount     int32
	IdentificationErrorCount int32
}

// PendingJob is a job awaiting processing, returned by ListPendingJobs.
type PendingJob struct {
	ID      string
	JobType string
}

// CreateJobParams holds the parameters for creating a new job.
type CreateJobParams struct {
	UserID     string
	JobType    string // "tx" or "price"
	Broker     string // tx only
	Source     string // tx only
	Filename   string
	PeriodFrom *timestamppb.Timestamp
	PeriodTo   *timestamppb.Timestamp
	Payload    []byte // serialized protobuf request
}

// JobDB provides ingestion job operations.
type JobDB interface {
	CreateJob(ctx context.Context, params CreateJobParams) (string, error)
	GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, []IdentificationError, string, int32, int32, error) // returns (status, validationErrors, idErrors, userID, totalCount, processedCount, error)
	SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error
	SetJobTotalCount(ctx context.Context, jobID string, total int32) error
	IncrJobProcessedCount(ctx context.Context, jobID string) error
	AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error
	AppendIdentificationErrors(ctx context.Context, jobID string, errs []IdentificationError) error
	LoadJobPayload(ctx context.Context, jobID string) ([]byte, error)
	ClearJobPayload(ctx context.Context, jobID string) error
	ListPendingJobs(ctx context.Context) ([]PendingJob, error)
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

// ProviderIdentifierInput is a provider-specific identifier for an instrument.
// Identifier types are free-form strings specific to the provider (e.g.
// "SEGMENT_MIC_TICKER", "EODHD_EXCH_CODE", "FIGI").
type ProviderIdentifierInput struct {
	Provider string
	Type     string
	Domain   string
	Value    string
}

// OptionFields carries denormalized OCC components for option instruments.
// Nil when the instrument is not an option.
type OptionFields struct {
	Strike  float64
	Expiry  time.Time
	PutCall string // "C" or "P"
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
	Synthetic             bool
	FetchedAt             time.Time
}

// ExportPriceRow is a single price row with the best instrument identifier for export.
type ExportPriceRow struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	Currency         string
	PriceDate        time.Time
	Open             *float64
	High             *float64
	Low              *float64
	Close            float64
	AdjustedClose    *float64
	Volume           *int64
}

// EODPriceListDB provides paginated listing of EOD prices for admin UI.
type EODPriceListDB interface {
	// ListPrices returns EOD prices with optional search, date range, and provider filters.
	// Returns (rows, totalCount, nextPageToken, error).
	ListPrices(ctx context.Context, search string, dateFrom, dateTo time.Time,
		dataProvider string, pageSize int32, pageToken string) ([]EODPriceRow, int32, string, error)
	// ListPricesForExport returns all EOD prices with the best identifier per instrument.
	// Instruments with no identifiers are excluded.
	ListPricesForExport(ctx context.Context) ([]ExportPriceRow, error)
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
	AssetClassFX          = "FX"
	AssetClassUnknown     = "UNKNOWN"
)

// ValidAssetClasses is the set of allowed asset_class values for validation.
var ValidAssetClasses = map[string]bool{
	AssetClassStock: true, AssetClassETF: true, AssetClassFixedIncome: true, AssetClassMutualFund: true,
	AssetClassOption: true, AssetClassFuture: true, AssetClassCash: true, AssetClassFX: true, AssetClassUnknown: true,
}

// AssetClassToStr converts a proto AssetClass enum to its DB string (e.g. ASSET_CLASS_STOCK -> "STOCK").
// ASSET_CLASS_UNSPECIFIED maps to "".
func AssetClassToStr(ac apiv1.AssetClass) string {
	if ac == apiv1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		return ""
	}
	return strings.TrimPrefix(ac.String(), "ASSET_CLASS_")
}

// StrToAssetClass converts a DB asset class string to its proto enum (e.g. "STOCK" -> ASSET_CLASS_STOCK).
func StrToAssetClass(s string) apiv1.AssetClass {
	v, ok := apiv1.AssetClass_value["ASSET_CLASS_"+s]
	if !ok {
		return apiv1.AssetClass_ASSET_CLASS_UNSPECIFIED
	}
	return apiv1.AssetClass(v)
}

// TxTypeToAssetClass maps a TxType to its asset class. Used for filtering and ignore rules.
func TxTypeToAssetClass(t apiv1.TxType) string {
	switch t {
	case apiv1.TxType_BUYDEBT, apiv1.TxType_SELLDEBT:
		return AssetClassFixedIncome
	case apiv1.TxType_BUYMF, apiv1.TxType_SELLMF:
		return AssetClassMutualFund
	case apiv1.TxType_BUYOPT, apiv1.TxType_SELLOPT, apiv1.TxType_CLOSUREOPT:
		return AssetClassOption
	case apiv1.TxType_BUYOTHER, apiv1.TxType_SELLOTHER:
		return AssetClassUnknown
	case apiv1.TxType_BUYSTOCK, apiv1.TxType_SELLSTOCK:
		return AssetClassStock
	case apiv1.TxType_BUYFUTURE, apiv1.TxType_SELLFUTURE:
		return AssetClassFuture
	case apiv1.TxType_INCOME, apiv1.TxType_INVEXPENSE,
		apiv1.TxType_MARGININTEREST, apiv1.TxType_RETOFCAP, apiv1.TxType_JRNLFUND,
		apiv1.TxType_CASHFLOW:
		return AssetClassCash
	case apiv1.TxType_TRANSFER, apiv1.TxType_REINVEST, apiv1.TxType_JRNLSEC, apiv1.TxType_SPLIT:
		return AssetClassUnknown
	default:
		return AssetClassUnknown
	}
}

// assetClassEquivalents lists unordered pairs of asset classes that brokers
// commonly conflate. The relation is intentionally non-transitive: STOCK and
// MUTUAL_FUND are not equivalent even though both are paired with ETF.
var assetClassEquivalents = map[[2]string]bool{
	{AssetClassStock, AssetClassETF}:      true,
	{AssetClassETF, AssetClassStock}:      true,
	{AssetClassMutualFund, AssetClassETF}: true,
	{AssetClassETF, AssetClassMutualFund}: true,
}

// IsAssetClassCompatible reports whether a transaction whose TxType implies
// asset class `implied` may legitimately be linked to an instrument with
// asset class `resolved`.
//
// Rules:
//   - When `resolved` is empty or UNKNOWN (the instrument's class is unset
//     or the identifier plugin could not classify it) there is no signal to
//     contradict, so the tx is accepted.
//   - When `implied` is UNKNOWN (TRANSFER, REINVEST, JRNLSEC, BUYOTHER,
//     SELLOTHER) the tx represents a security position; any concrete class is
//     accepted but CASH is rejected.
//   - STOCK <-> ETF and MUTUAL_FUND <-> ETF are treated as equivalent
//     (non-transitive: STOCK and MUTUAL_FUND remain incompatible).
//   - Otherwise, compatible iff the two strings are equal.
func IsAssetClassCompatible(implied, resolved string) bool {
	if resolved == "" || resolved == AssetClassUnknown {
		return true
	}
	if implied == AssetClassUnknown {
		return resolved != AssetClassCash
	}
	if implied == resolved {
		return true
	}
	return assetClassEquivalents[[2]string{implied, resolved}]
}

// InstrumentKind constants. Coarser than asset class; used as a first-pass
// filter to separate cash from securities during plugin routing.
const (
	InstrumentKindCash     = "CASH"
	InstrumentKindSecurity = "SECURITY"
)

// TxTypeToInstrumentKind maps a TxType to its instrument kind. CASH kinds are
// cash-flow transactions (dividends, fees, etc). SECURITY kinds represent
// positions in instruments that need identification and pricing.
func TxTypeToInstrumentKind(t apiv1.TxType) string {
	switch t {
	case apiv1.TxType_INCOME, apiv1.TxType_INVEXPENSE,
		apiv1.TxType_MARGININTEREST, apiv1.TxType_RETOFCAP, apiv1.TxType_JRNLFUND,
		apiv1.TxType_CASHFLOW:
		return InstrumentKindCash
	default:
		return InstrumentKindSecurity
	}
}

// AssetClassToTxTypeStrings returns the tx_type DB strings that map to the given asset class.
func AssetClassToTxTypeStrings(assetClass string) []string {
	var strs []string
	for i := range apiv1.TxType_name {
		t := apiv1.TxType(i)
		if t == apiv1.TxType_TX_TYPE_UNSPECIFIED {
			continue
		}
		if TxTypeToAssetClass(t) == assetClass {
			strs = append(strs, t.String())
		}
	}
	return strs
}

// InstrumentRow is a single instrument with its identifiers (for API responses).
// Nullable DB columns use pointer types; nil means NULL.
type InstrumentRow struct {
	ID                  string
	AssetClass          *string
	ExchangeMIC         *string
	Currency            *string
	Name                *string
	Exchange            string  // denormalized; trigger-computed from acronym/identifier
	UnderlyingID        *string
	ValidFrom           *time.Time
	ValidTo             *time.Time
	CIK                 *string
	SICCode             *string
	Strike              *float64   // denormalized from OCC; NULL for non-options
	Expiry              *time.Time // denormalized from OCC; NULL for non-options
	PutCall             *string    // "C" or "P"; NULL for non-options
	ContractMultiplier  float64    // deliverable multiplier; 1 = standard
	IdentifiedAt        *time.Time // last identification timestamp
	Identifiers         []IdentifierInput
	ProviderIdentifiers []ProviderIdentifierInput // provider-specific identifiers
	ExchangeName        *string                   // read-only; from exchanges JOIN
	ExchangeAcronym     *string                   // read-only; from exchanges JOIN
	ExchangeCountryCode *string                   // read-only; from exchanges JOIN
}

// HoldingDeclarationRow is a single holding declaration for API responses.
type HoldingDeclarationRow struct {
	ID           string
	UserID       string
	Broker       string
	Account      string
	InstrumentID string
	DeclaredQty  string // numeric as string to preserve precision
	AsOfDate     time.Time
}

// HoldingDeclarationDB provides holding declaration CRUD and INITIALIZE tx helpers.
type HoldingDeclarationDB interface {
	CreateHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate time.Time) (*HoldingDeclarationRow, error)
	UpdateHoldingDeclaration(ctx context.Context, id, declaredQty string, asOfDate time.Time) (*HoldingDeclarationRow, error)
	DeleteHoldingDeclaration(ctx context.Context, id string) error
	GetHoldingDeclaration(ctx context.Context, id string) (*HoldingDeclarationRow, error)
	ListHoldingDeclarations(ctx context.Context, userID string) ([]*HoldingDeclarationRow, error)
	// GetPortfolioStartDate returns the earliest real tx timestamp for the user, or nil if none exist.
	GetPortfolioStartDate(ctx context.Context, userID string) (*time.Time, error)
	// ComputeRunningBalance sums quantity of real (non-synthetic) txs for the given holding where timestamp >= from and timestamp < to.
	ComputeRunningBalance(ctx context.Context, userID, broker, account, instrumentID string, from, to time.Time) (float64, error)
	// UpsertInitializeTx creates or updates the INITIALIZE synthetic tx for the given holding. txType is the OFX type string (e.g. "BUYSTOCK", "SELLOPT").
	UpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID, txType string, timestamp time.Time, quantity float64) error
	// DeleteInitializeTx deletes the INITIALIZE synthetic tx for the given holding, if it exists.
	DeleteInitializeTx(ctx context.Context, userID, broker, account, instrumentID string) error
	// CreateDeclarationWithInitializeTx atomically creates a declaration and upserts its INITIALIZE tx.
	CreateDeclarationWithInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate time.Time, initTxType string, initTimestamp time.Time, initQty float64) (*HoldingDeclarationRow, error)
	// UpdateDeclarationWithInitializeTx atomically updates a declaration and upserts its INITIALIZE tx.
	UpdateDeclarationWithInitializeTx(ctx context.Context, id, declaredQty string, asOfDate time.Time, userID, broker, account, instrumentID, initTxType string, initTimestamp time.Time, initQty float64) (*HoldingDeclarationRow, error)
	// DeleteDeclarationWithInitializeTx atomically deletes a declaration and its INITIALIZE tx.
	DeleteDeclarationWithInitializeTx(ctx context.Context, id, userID, broker, account, instrumentID string) error
}

// InstrumentDB provides instrument resolution and plugin config.
type InstrumentDB interface {
	// EnsureInstrument finds an instrument by any of the given identifiers, or creates one with the given canonical fields and identifiers. Returns instrument ID. On unique violation (identifier already exists for another instrument), merges and returns the existing instrument ID. When assetClass is OPTION or FUTURE, underlyingID must be non-empty. exchangeMIC is the ISO 10383 MIC code (nullable). optionFields is non-nil only for OPTION instruments and supplies denormalized OCC components.
	EnsureInstrument(ctx context.Context, assetClass, exchangeMIC, currency, name, cik, sicCode string, identifiers []IdentifierInput, underlyingID string, validFrom, validTo *time.Time, optionFields *OptionFields) (string, error)
	// FindInstrumentByIdentifier looks up instrument_id by (identifier_type, domain, value). Returns "" if not found. Use empty domain for no domain.
	FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error)
	// FindInstrumentWithMetaByIdentifier is like FindInstrumentByIdentifier but also returns asset_class, exchange_mic (ISO 10383 MIC code), and currency from the instruments table in one query.
	FindInstrumentWithMetaByIdentifier(ctx context.Context, identifierType, domain, value string) (instrumentID, assetClass, exchangeMIC, currency string, err error)
	// FindInstrumentByTypeAndValue looks up instrument_id by (identifier_type, value) with any domain. Returns "" if not found or if multiple instruments match (ambiguous).
	FindInstrumentByTypeAndValue(ctx context.Context, identifierType, value string) (string, error)
	// FindInstrumentBySourceDescription looks up instrument_id by (source, NULL domain, instrument_description). Returns "" if not found.
	FindInstrumentBySourceDescription(ctx context.Context, source, description string) (string, error)
	// GetInstrument returns an instrument by ID with its identifiers, or nil if not found.
	GetInstrument(ctx context.Context, instrumentID string) (*InstrumentRow, error)
	// ListInstrumentsByIDs returns instruments by ID slice (for batch underlying lookup). Missing IDs are omitted; order not guaranteed.
	ListInstrumentsByIDs(ctx context.Context, ids []string) ([]*InstrumentRow, error)
	// ListInstrumentsForExport returns all instruments that have at least one identifier with canonical = true. Excludes CASH and FX asset classes (reference data). If exchangeFilter != "", filter by instruments.exchange_mic. Order by instruments.id.
	ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*InstrumentRow, error)
	// ValidateMIC checks whether the given MIC code exists in the exchanges reference table.
	ValidateMIC(ctx context.Context, mic string) (bool, error)
	// ListInstruments returns instruments sorted alphabetically by display name (ticker, then name, then broker description). If search is non-empty, only instruments with at least one identifier value matching (case-insensitive substring) are returned. If assetClasses is non-empty, only instruments with matching asset_class are returned. Returns (rows, totalCount, nextPageToken, error).
	ListInstruments(ctx context.Context, search string, assetClasses []string, pageSize int32, pageToken string) ([]*InstrumentRow, int32, string, error)

	// ListOptionsByUnderlying returns all OPTION instruments with the given
	// underlying_id, including their identifiers.
	ListOptionsByUnderlying(ctx context.Context, underlyingID string) ([]*InstrumentRow, error)
	// DeleteInstrumentIdentifier removes a single identifier row by
	// (instrument_id, identifier_type, value). Returns nil when no row exists.
	DeleteInstrumentIdentifier(ctx context.Context, instrumentID, identifierType, value string) error
	// InsertInstrumentIdentifier inserts a single identifier row.
	InsertInstrumentIdentifier(ctx context.Context, instrumentID string, input IdentifierInput) error
	// UpdateInstrumentStrike updates the strike on an existing option instrument.
	UpdateInstrumentStrike(ctx context.Context, instrumentID string, strike float64) error
	// UpdateInstrumentName updates the name on an existing instrument.
	UpdateInstrumentName(ctx context.Context, instrumentID, name string) error
	// UpdateIdentifiedAt sets identified_at = now() on an existing instrument.
	UpdateIdentifiedAt(ctx context.Context, instrumentID string) error
	// SaveProviderIdentifiers inserts provider-specific identifiers for an instrument.
	// Duplicates (same instrument, provider, type, domain, value) are silently ignored.
	SaveProviderIdentifiers(ctx context.Context, instrumentID string, ids []ProviderIdentifierInput) error
	// FindProviderIdentifiers returns provider-specific identifiers for an instrument and provider.
	FindProviderIdentifiers(ctx context.Context, instrumentID, provider string) ([]ProviderIdentifierInput, error)
	// LookupOperatingMIC returns the operating MIC for the given MIC code.
	// If mic is already an operating MIC it returns itself. Returns ("", error) if not found.
	LookupOperatingMIC(ctx context.Context, mic string) (string, error)
}

// IgnoredAssetClass is one ignore rule: skip tx types mapping to this asset class for
// the given broker (and optionally account). Account="" means all accounts.
type IgnoredAssetClass struct {
	Broker     string
	Account    string // empty = all accounts for broker
	AssetClass string
}

// InflationIndex is a single monthly inflation index value.
type InflationIndex struct {
	Currency     string
	Month        time.Time // 1st of month, UTC
	IndexValue   float64
	BaseYear     int
	DataProvider string
}

// InflationIndexDB provides inflation index storage and querying.
type InflationIndexDB interface {
	// DistinctDisplayCurrencies returns the set of display currencies across all users.
	DistinctDisplayCurrencies(ctx context.Context) ([]string, error)
	// InflationCoverage returns months with inflation data for the given currency, ordered ascending.
	InflationCoverage(ctx context.Context, currency string) ([]time.Time, error)
	// UpsertInflationIndices inserts or updates monthly inflation index values.
	// On conflict (currency, month), overwrites with new data.
	UpsertInflationIndices(ctx context.Context, indices []InflationIndex) error
	// ListInflationIndices returns inflation data for admin UI listing with pagination.
	// currency is an optional filter (empty = all). dateFrom/dateTo are optional date range filters.
	// Returns (rows, nextPageToken, totalCount, error).
	ListInflationIndices(ctx context.Context, currency string, dateFrom, dateTo *time.Time, pageSize int, pageToken string) ([]InflationIndex, string, int, error)
}

// IgnoredAssetClassDB manages per-broker/account asset class ignore rules.
type IgnoredAssetClassDB interface {
	// ListIgnoredAssetClasses returns all ignore rules for the user.
	ListIgnoredAssetClasses(ctx context.Context, userID string) ([]IgnoredAssetClass, error)
	// SetIgnoredAssetClasses replaces all ignore rules for the user and deletes
	// matching txs, synthetic INITIALIZE txs, and holding declarations atomically.
	// assetClassToTxTypes maps each asset class to its tx_type DB strings.
	SetIgnoredAssetClasses(ctx context.Context, userID string, rules []IgnoredAssetClass, assetClassToTxTypes map[string][]string) error
	// CountIgnoredTxs returns the number of regular txs and holding declarations
	// that would be deleted if the given rules were applied (net new vs current).
	CountIgnoredTxs(ctx context.Context, userID string, rules []IgnoredAssetClass, assetClassToTxTypes map[string][]string) (txCount int32, declCount int32, err error)
}

// StockSplit is a single stock split row. SplitFrom and SplitTo are the raw
// halves of the split ratio (factor = SplitTo / SplitFrom). They are stored as
// strings so that NUMERIC values from the database round-trip without loss of
// precision; conversion to float happens at the math boundary.
type StockSplit struct {
	InstrumentID string
	ExDate       time.Time
	SplitFrom    string // numeric, e.g. "1"
	SplitTo      string // numeric, e.g. "2"
	DataProvider string
	FetchedAt    time.Time
}

// CashDividend is a single cash dividend row. Amount is per share in Currency.
// PayDate, RecordDate, DeclarationDate, and Frequency are optional and may be
// nil/empty when the provider does not supply them.
type CashDividend struct {
	InstrumentID    string
	ExDate          time.Time
	PayDate         *time.Time
	RecordDate      *time.Time
	DeclarationDate *time.Time
	Amount          string // numeric, e.g. "0.24"
	Currency        string
	Frequency       string // empty when unknown
	DataProvider    string
	FetchedAt       time.Time
}

// CorporateEventCoverage is one coverage interval for a (instrument, plugin).
// Adjacent or overlapping intervals for the same (InstrumentID, PluginID) are
// merged on insert by UpsertCorporateEventCoverage.
type CorporateEventCoverage struct {
	InstrumentID string
	PluginID     string
	CoveredFrom  time.Time
	CoveredTo    time.Time
	FetchedAt    time.Time
}

// CorporateEventFetchBlock records a permanently blocked (instrument, plugin)
// pair for corporate-event fetches. Mirrors PriceFetchBlock.
type CorporateEventFetchBlock struct {
	InstrumentID string
	PluginID     string
	Reason       string
	CreatedAt    time.Time
}

// UnhandledCorporateEvent is a corporate event that cannot be automatically
// processed (reverse splits, non-whole splits, mergers, extraordinary
// dividends on options, futures adjustments). Surfaced to admins.
type UnhandledCorporateEvent struct {
	ID           string
	InstrumentID string
	EventType    string
	ExDate       *time.Time
	Detail       string
	Data         []byte // JSONB
	Resolved     bool
	CreatedAt    time.Time
}

// OptionSplitParams bundles the mutations needed to adjust a single option
// contract after a stock split on its underlying.
type OptionSplitParams struct {
	InstrumentID string
	OldOCCValue  string
	NewOCC       IdentifierInput
	NewStrike    float64
	NewName      string
}

// CorporateEventDB provides storage for stock splits, cash dividends, fetch
// coverage, fetch blocks, and the recompute primitive that derives the
// split_adjusted_* columns on eod_prices and txs from the raw values.
type CorporateEventDB interface {
	// UpsertStockSplits inserts or updates the supplied stock_splits rows.
	// On conflict (instrument_id, ex_date), all non-key columns are overwritten.
	UpsertStockSplits(ctx context.Context, splits []StockSplit) error
	// ListStockSplits returns every stock split for the given instrument
	// ordered ascending by ex_date.
	ListStockSplits(ctx context.Context, instrumentID string) ([]StockSplit, error)
	// DeleteStockSplit removes a single (instrument, ex_date) row. Returns
	// nil even when no row exists; callers that need an "exists" signal should
	// check ListStockSplits first.
	DeleteStockSplit(ctx context.Context, instrumentID string, exDate time.Time) error

	// UpsertCashDividends inserts or updates the supplied cash_dividends rows.
	// On conflict (instrument_id, ex_date), all non-key columns are overwritten.
	UpsertCashDividends(ctx context.Context, dividends []CashDividend) error
	// ListCashDividends returns every cash dividend for the given instrument
	// ordered ascending by ex_date.
	ListCashDividends(ctx context.Context, instrumentID string) ([]CashDividend, error)
	// DeleteCashDividend removes a single (instrument, ex_date) row.
	DeleteCashDividend(ctx context.Context, instrumentID string, exDate time.Time) error

	// UpsertCorporateEventCoverage records that (instrumentID, pluginID) has
	// been queried for the closed interval [from, to]. Existing rows for the
	// same (instrument, plugin) that are adjacent or overlap with [from, to]
	// are merged into a single row.
	UpsertCorporateEventCoverage(ctx context.Context, instrumentID, pluginID string, from, to time.Time) error
	// ListCorporateEventCoverage returns coverage rows for the given
	// instruments. When instrumentIDs is empty all coverage rows are returned.
	// Rows are sorted by (instrument_id, plugin_id, covered_from).
	ListCorporateEventCoverage(ctx context.Context, instrumentIDs []string) ([]CorporateEventCoverage, error)

	// CreateCorporateEventFetchBlock blocks (instrument, plugin) for future
	// corporate-event fetch attempts. Idempotent on (instrument_id, plugin_id).
	CreateCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error
	// DeleteCorporateEventFetchBlock removes a block; nil when no row exists.
	DeleteCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID string) error
	// ListCorporateEventFetchBlocks returns every block row.
	ListCorporateEventFetchBlocks(ctx context.Context) ([]CorporateEventFetchBlock, error)
	// BlockedCorporateEventPluginsForInstruments returns blocked plugin IDs
	// keyed by instrument id, mirroring PriceFetchBlockDB.
	BlockedCorporateEventPluginsForInstruments(ctx context.Context, instrumentIDs []string) (map[string]map[string]bool, error)

	// RecomputeSplitAdjustments recomputes split_adjusted_* on eod_prices and
	// txs for the given instrument from the raw columns and the current set of
	// stock_splits rows. Idempotent: running it twice produces identical state.
	// When instrumentID is empty, every instrument with at least one
	// stock_splits row is recomputed.
	RecomputeSplitAdjustments(ctx context.Context, instrumentID string) error

	// HeldEventBearingInstruments returns one row per instrument that needs
	// corporate event coverage: directly held STOCK/ETF instruments, plus
	// underlyings of held OPTION/FUTURE instruments. For underlyings
	// discovered via derivatives, the earliest tx date is the minimum across
	// all derivatives on that underlying.
	HeldEventBearingInstruments(ctx context.Context) ([]HeldInstrument, error)

	// ListStockSplitsForExport returns every stock_splits row joined with the
	// best identifier per instrument (MIC_TICKER > OPENFIGI_TICKER > ISIN > ...),
	// using the same priority logic as ListPricesForExport. Instruments with
	// no identifiers are excluded.
	ListStockSplitsForExport(ctx context.Context) ([]ExportStockSplit, error)

	// ListCashDividendsForExport returns every cash_dividends row joined with
	// the best identifier per instrument. See ListStockSplitsForExport for the
	// identifier priority order.
	ListCashDividendsForExport(ctx context.Context) ([]ExportCashDividend, error)

	// SplitsByUnderlyingTicker returns stock splits for the instrument matching
	// the given MIC_TICKER, ordered ascending by ex_date. Used by split-aware
	// identification to adjust pre-split OCC identifiers.
	SplitsByUnderlyingTicker(ctx context.Context, ticker string) ([]StockSplit, error)
	// InstrumentsWithSplits returns the subset of instrumentIDs that have at
	// least one stock_splits row.
	InstrumentsWithSplits(ctx context.Context, instrumentIDs []string) ([]string, error)

	// ApplyOptionSplit atomically adjusts an option contract for a stock
	// split on its underlying: replaces the OCC identifier, updates the
	// strike, inserts a derived split row, recomputes split-adjusted tx
	// values, and updates identified_at. All mutations run in a single
	// transaction so partial failure cannot leave the option inconsistent.
	ApplyOptionSplit(ctx context.Context, params OptionSplitParams) error

	// InsertUnhandledCorporateEvent stores a corporate event that requires
	// manual admin review. Duplicate unresolved (instrument_id, event_type,
	// ex_date) rows are silently ignored via ON CONFLICT DO NOTHING.
	InsertUnhandledCorporateEvent(ctx context.Context, event UnhandledCorporateEvent) error
	// ListUnhandledCorporateEvents returns unhandled events, newest first.
	// When includeResolved is false, only unresolved events are returned.
	ListUnhandledCorporateEvents(ctx context.Context, includeResolved bool, pageSize int32, pageToken string) ([]UnhandledCorporateEvent, int32, string, error)
	// CountUnhandledCorporateEvents returns the number of unresolved events.
	CountUnhandledCorporateEvents(ctx context.Context) (int32, error)
	// ResolveUnhandledCorporateEvent marks an event as resolved.
	ResolveUnhandledCorporateEvent(ctx context.Context, id string) error
}

// HeldInstrument is one held instrument with the date of its earliest tx.
type HeldInstrument struct {
	InstrumentID   string
	EarliestTxDate time.Time
}

// ExportStockSplit is one stock split row with the best identifier for the
// instrument, used by ExportCorporateEvents.
type ExportStockSplit struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	DataProvider     string
	ExDate           time.Time
	SplitFrom        string // numeric as decimal string
	SplitTo          string
}

// ExportCashDividend is one cash dividend row with the best identifier for
// the instrument, used by ExportCorporateEvents.
type ExportCashDividend struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	DataProvider     string
	ExDate           time.Time
	PayDate          *time.Time
	RecordDate       *time.Time
	DeclarationDate  *time.Time
	Amount           string
	Currency         string
	Frequency        string
}
