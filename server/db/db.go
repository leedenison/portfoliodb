package db

//go:generate go run go.uber.org/mock/mockgen -source=db.go -destination=mock/db_mock.go -package=mock

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

// ErrDuplicate reports that a write collided with a uniqueness constraint. The db
// layer translates the driver's error into this so a caller can react to it without
// knowing which driver, or which constraint, produced it.
var ErrDuplicate = errors.New("duplicate")

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

// PriceProviderImport tags price coverage that came from an import rather than a
// plugin, so the fetcher does not treat a hand-curated range as one of its own.
const PriceProviderImport = "import"

// InflationProviderImport tags inflation index rows that came from an import
// rather than a plugin. It is the same sentinel for the same reason.
const InflationProviderImport = "import"

// Job type constants for the ingestion_jobs table.
const (
	JobTypeTx            = "tx"
	JobTypeSystemArchive = "system_archive"
	JobTypeUserArchive   = "user_archive"
)

// DB is the database abstraction used by the service layer.
type DB interface {
	UserDB
	ServiceAccountDB
	PortfolioDB
	TxDB
	HoldingsDB
	ValuationDB
	ExternalFlowDB
	JobDB
	InstrumentDB
	PluginConfigDB
	PriceCacheDB
	PriceFetchBlockDB
	EODPriceListDB
	HoldingDeclarationDB
	InflationIndexDB
	CorporateEventDB
	ResidualBalanceDB
	TransferMatchDB
	GroupingDB
}

// PriceFetchBlockDB manages permanently blocked (instrument, plugin) pairs.
type PriceFetchBlockDB interface {
	ListPriceFetchBlocks(ctx context.Context) ([]PriceFetchBlock, error)
	// BlockedPluginsForInstruments returns blocked plugin IDs keyed by instrument ID.
	BlockedPluginsForInstruments(ctx context.Context, instrumentIDs []string) (map[string]map[string]bool, error)
	CreatePriceFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error
	DeletePriceFetchBlock(ctx context.Context, instrumentID, pluginID string) error
	// ListPriceFetchBlocksForExport returns every price fetch block with the best
	// identifier per instrument, for the archive's fetch block part.
	ListPriceFetchBlocksForExport(ctx context.Context) ([]ExportFetchBlock, error)
	// UpsertPriceFetchBlocks restores blocks with the knowledge time they carry.
	// On conflict the reason is replaced and first_blocked_at keeps the earlier
	// of the stored and the supplied value, because it records when the pair was
	// first blocked and is never overwritten.
	UpsertPriceFetchBlocks(ctx context.Context, blocks []FetchBlockInput) error
}

// DateRange is a half-open [From, Before) date range. Both values are midnight
// UTC. Every date interval in this codebase is half-open and names its
// exclusive bound Before; see docs/adr/0018-half-open-date-intervals.md.
type DateRange struct {
	From   time.Time // inclusive
	Before time.Time // exclusive
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
	Open         *decimal.Decimal
	High         *decimal.Decimal
	Low          *decimal.Decimal
	Close        decimal.Decimal
	Volume       *int64
	DataProvider string
	// AdjustedClose is the provider's own adjusted close, on the provider's
	// basis and typically including dividend adjustment. Stored for cross-checking
	// only; never an input to valuation. nil when the provider does not supply it.
	AdjustedClose *decimal.Decimal
	LastFetchedAt *time.Time // when this row was fetched; nil defaults to now()
	// ShareCountBasis is the date at which the share count these raw values are
	// denominated in was current. nil defaults to PriceDate (as-traded).
	ShareCountBasis *time.Time
}

// PriceCacheDB provides price cache management.
type PriceCacheDB interface {
	// HeldRanges computes system-wide date ranges during which any user held
	// a non-zero position in each identified instrument.
	HeldRanges(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// PriceCoverage returns the date ranges some plugin has answered for, merged
	// across plugins. A range covered with no bars counts: it records that a
	// provider was asked and had nothing, which row presence cannot express.
	// If instrumentIDs is non-empty, only those instruments are returned.
	PriceCoverage(ctx context.Context, instrumentIDs []string) ([]InstrumentDateRanges, error)
	// PriceCoverageByPlugin returns the same spans keyed instrument -> plugin ->
	// ranges, for deciding what to ask each plugin rather than whether anyone has
	// answered at all.
	PriceCoverageByPlugin(ctx context.Context, instrumentIDs []string) (map[string]map[string][]DateRange, error)
	// PriceGaps computes needed ranges minus covered ranges per instrument.
	PriceGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// FXGaps computes date ranges where FX rates are needed (non-USD instruments
	// are held) but not yet cached. Returns gaps keyed by FX pair instrument ID.
	FXGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
	// UpsertPrices inserts or updates EOD prices, each covering its own date.
	// Use it when the caller has no range to declare beyond the days it names.
	UpsertPrices(ctx context.Context, prices []EODPrice) error
	// UpsertPricesForRange stores bars and records [from, before) as coverage in
	// one transaction, whether or not any bars came back. Days in the range with
	// no bar stay absent: the carry-forward is applied at read time and bounded
	// by this coverage, so it is never stored.
	UpsertPricesForRange(ctx context.Context, instrumentID, provider string, bars []EODPrice, from, before time.Time, fetchedAt *time.Time) error
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
	// ListAllPluginConfigs returns every plugin config row across every
	// category, ordered by category then precedence descending. Used by the
	// archive, which has no one category to ask about.
	ListAllPluginConfigs(ctx context.Context) ([]PluginConfigWithCategory, error)
	// RestorePluginConfigs applies an archive's plugin configuration, one
	// category at a time. Every named (category, plugin_id) must already have a
	// row -- the caller is expected to have rejected the rest -- because a
	// config row for a plugin this build does not register is one nothing will
	// ever read.
	//
	// A category's rows are applied as a set: the file's precedences are used
	// exactly, and a plugin the file does not name is moved below all of them
	// rather than left where it was, so an unnamed plugin cannot end up
	// preferred over the restored ordering.
	RestorePluginConfigs(ctx context.Context, configs []PluginConfigWithCategory) error
	// ReorderPluginConfigs sets precedence for all plugins in a category.
	// pluginIDs is ordered from highest to lowest precedence. All existing
	// plugin IDs for the category must be present.
	ReorderPluginConfigs(ctx context.Context, category string, pluginIDs []string) error
}

// IdentificationError is stored per job for identification warnings (e.g. broker description only, plugin timeout).
type IdentificationError struct {
	RowIndex              int32
	InstrumentDescription string
	Message               string
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

// Weight is what a posting contributes to its group's balance, and the commodity it
// contributes in. A group balances when its postings' weights sum to zero in every
// commodity.
//
// The weight rules -- which tx types convert, the settlement-currency guard, the
// contract-size multiplication -- live in server/service/ingestion/balance.go and are
// evaluated once, at ingest. The value is then stored on the posting rather than
// re-derived on read, because instrument state moves under a posting afterwards: a
// merge rewrites instrument_id wholesale and contract_multiplier records what a
// corporate action left behind, so a re-derived weight could disagree with the one
// the group was balanced on. See docs/adr/0029-posting-weight-is-stored.md.
//
// Commodity is a prefixed name rather than an id, because the three kinds of
// commodity a weight can be in are not the same kind of thing: "cur:USD" for money,
// "inst:<uuid>" for a security, and "desc:<description>" for a posting whose
// instrument never resolved. It is never empty, so an unresolved posting still
// balances against itself.
type Weight struct {
	Amount    decimal.Decimal
	Commodity string
}

// Correlation is a stored statement of why a posting might belong with another
// one: an identifier its source issued, what may be compared about it, and over
// what set of postings. See
// docs/adr/0048-correlations-declare-their-own-semantics.md.
//
// Ordinal and OrdinalSpan are pointers because a source whose identifiers are
// opaque supplies neither, and a zero ordinal is a position rather than an
// absence. Scope and Match hold the stored vocabulary -- "FILE", "EXACT" -- so
// one string spells the value in the proto, in the column and in an archive
// file.
//
// JobID is the ingestion job that supplied this, and is what a FILE-scoped
// correlation is comparable within. It is not written to an archive: another
// instance's job ids mean nothing, and an import stamps its own.
type Correlation struct {
	Label       string
	Token       string
	Ordinal     *int64
	Scope       string
	Match       []string
	OrdinalSpan *int64
	JobID       string
}

// ExportPosting is one stored posting with the best identifier of the instrument
// it names, plus the group identity an archive needs to nest it.
//
// GroupID and GroupTimestamp are here to group and order the scan, not to be
// written: a group id means nothing in another instance, and a group's timestamp
// is the timestamp of the first posting that names it, so both are derived. See
// docs/adr/0035-archive-nests-by-aggregate-root.md.
type ExportPosting struct {
	Broker string
	// The posting's own id. Not written to a file -- it means nothing in another
	// instance, as a group id does not -- but the correlations are read in a
	// second pass and this is what attaches them to their posting.
	ID             string
	GroupID        string
	GroupTimestamp time.Time
	OrderDate      time.Time
	TradeDate      time.Time
	Account        string
	AccountType    string
	// The declared candidate set. The resolved value is deliberately not
	// exported: an import re-derives the grouping the resolution depends on.
	BrokerTxTypes []string
	// The stated routing hint, empty when the source made no claim.
	AssetClassHint string
	Description    string
	// The instrument's best identifier, or all three empty for a posting whose
	// instrument never resolved. Chosen by bestIdentifierJoin so that every
	// export naming one identifier per instrument agrees which one.
	IdentifierType     string
	IdentifierValue    string
	IdentifierDomain   string
	Quantity           decimal.Decimal
	UnitPrice          *decimal.Decimal
	TradingCurrency    string
	SettlementCurrency string
	// The cash total the source stated for the row, or nil on a posting whose
	// own quantity is already money.
	SettlementAmount *decimal.Decimal
	// The share count this posting is denominated in, or nil when it is the
	// posting's own trade date. The column is NOT NULL and the insert
	// trigger defaults it to that date, so only a value that differs from it
	// says anything, and only that value is worth writing to a file.
	ShareCountBasis *time.Time
	// Why this posting might belong with another one, in the order its source
	// stated them. Empty for a derived posting, which transcribes nothing.
	Correlations []Correlation
}

// The values synthetic_purpose takes on a posting the server made, as against the
// NULL a posting a source stated carries.
//
// It is what separates derived from stated, and account_type is not: a routed
// residual and a leg a converter read out of a record both land in a non-USER
// account type, so an account-type list cannot tell one from the other. A posting
// with a purpose is recreated whenever the group it sits in changes; one without is
// an input and survives.
const (
	// RoutedPurpose is what a group's legs did not balance to, posted to an
	// explicit counterparty so the group sums to zero.
	RoutedPurpose = "RESIDUAL"
	// BoundaryPurpose is the other side of a posting whose own type names where
	// its money came from or went to: income for a dividend, expense for a
	// charge. Derived from the posting alone rather than from the group, but
	// recreated with the residuals because it has to move with the leg it
	// mirrors. See docs/adr/0022-typed-per-account-cash-flow-boundary.md.
	BoundaryPurpose = "BOUNDARY"
	// InitializePurpose is a synthetic opening balance, derived from a holding
	// declaration rather than from the group it sits in.
	InitializePurpose = "INITIALIZE"
)

// TxDB provides transaction write, list and export.
//
// The write methods take the postings a source stated and nothing else. What a group
// owes -- the other side of a one-sided cash row, and whatever is still left over --
// is the store's, written from the stored weights once the postings are in and in
// the same transaction, so no caller can leave a group unbalanced and none of them
// can disagree about what balancing means.
type TxDB interface {
	// Every posting is written into a group of its own and then partitioned by the
	// Settler, so what comes back is the engine's answer rather than the shape the
	// postings arrived in. Every group is stamped with the ingestion job that
	// created it.
	//
	// shareCountBasis is parallel to txs and is the date each row's quantity and
	// unit price are denominated in. A nil entry, and a nil slice, mean as-traded:
	// the row uses its own timestamp. It is per row rather than per call because a
	// file can restate one row and leave its neighbours alone.
	//
	// weights is parallel to txs and carries what each posting contributes to its
	// group's balance. A nil slice means the caller has none, and each posting then
	// weighs its own quantity in its own instrument -- which is what the weight rule
	// returns for a posting with no price. What the weights leave over is what the
	// store routes a counterparty for, so a caller that supplies none is asking for
	// one to be routed against that default.
	ReplaceTxsInPeriod(ctx context.Context, userID, broker, jobID string, periodFrom, periodBefore *timestamppb.Timestamp, txs []*apiv1.Tx, instrumentIDs []string, weights []Weight, shareCountBasis []*time.Time) error
	// CreateTxGroup appends the postings of one economic event as a single group,
	// rather than one posting as a group of its own. It takes a slice because the
	// legs of one event have to arrive together to be grouped together; what the
	// group owes is settled from them.
	CreateTxGroup(ctx context.Context, userID, broker, account, jobID string, txs []*apiv1.Tx, instrumentIDs []string, weights []Weight, shareCountBasis []*time.Time) error
	// ListTxs and ListTxsByPortfolio page by group rather than by posting: pageSize
	// counts groups, a page carries every posting of the groups it covers, and the
	// postings of one group are contiguous in the result. A group whose legs
	// straddled a page boundary would reach a client as two partial events, so the
	// page is a whole number of events instead.
	//
	// The filters select postings, not groups. A group is on the page when at least
	// one of its postings passes them and only the postings that passed are
	// returned, so a group straddling a period bound contributes its in-period legs
	// and a portfolio view carries the legs its filters matched.
	ListTxs(ctx context.Context, userID string, broker *typev1.Broker, account string, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
	ListTxsByPortfolio(ctx context.Context, portfolioID string, broker *typev1.Broker, periodFrom, periodBefore *timestamppb.Timestamp, descending bool, pageSize int32, pageToken string) ([]*apiv1.PortfolioTx, string, error)
	// ListTxsForExport reads one user's own postings in archive order: by broker,
	// then by group, then by posting. Synthetic INITIALIZE groups are excluded --
	// they are derived from holding declarations, which the archive carries as the
	// declarations they come from.
	//
	// The half-open [periodFrom, periodBefore) bounds are optional and filter the
	// postings rather than the groups holding them, so a group straddling a bound
	// contributes only its in-period legs. A nil bound is open-ended.
	ListTxsForExport(ctx context.Context, userID string, periodFrom, periodBefore *timestamppb.Timestamp) ([]ExportPosting, error)
}

// HoldingsDB computes holdings at a point in time.
type HoldingsDB interface {
	ComputeHoldings(ctx context.Context, userID string, broker *typev1.Broker, account string, asOf *timestamppb.Timestamp) ([]*apiv1.Holding, *timestamppb.Timestamp, error)
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
	GetPortfolioValuation(ctx context.Context, portfolioID string, dateFrom, dateBefore time.Time, displayCurrency string) ([]ValuationPoint, error)
	GetUserValuation(ctx context.Context, userID string, dateFrom, dateBefore time.Time, displayCurrency string) ([]ValuationPoint, error)
}

// ExternalFlow is one day's net external flow into a portfolio in one commodity.
//
// Positive is value entering: an opening balance, a deposit, or a transfer whose
// other side is not a member. The commodity is the flow's own -- a currency for
// money, the security for an in-specie transfer or an INITIALIZE pad -- and is not
// converted to a display currency. Converting would need a price as well as an FX
// rate, which is the whole of the valuation query, and would leave the exact decimals
// for a double (docs/adr/0026-exact-decimals-bounded-by-closure.md).
//
// InstrumentDescription is set only where the commodity never resolved to an
// instrument, which is a flow nothing can value. It is reported rather than dropped,
// so a consumer can say so instead of silently understating.
type ExternalFlow struct {
	Date                  time.Time
	InstrumentID          string
	InstrumentDescription string
	Amount                decimal.Decimal
}

// ExternalFlowDB reads the flows crossing a portfolio's cash-flow boundary over a
// half-open [dateFrom, dateBefore) window. What crosses it is defined in
// docs/spec/postings.md and docs/adr/0022-typed-per-account-cash-flow-boundary.md: an
// EQUITY leg always, a USER leg the portfolio does not match, and a TRANSFER_CLEARING
// leg that is unmatched or whose counterpart is not a member. A matched pair whose two
// accounts are both members nets to nothing, which is what this exists for.
//
// The window bounds the flows, not the groups they belong to: a group's legs need not
// share a date, so a group whose member leg falls outside the window can still have an
// external leg inside it.
type ExternalFlowDB interface {
	GetPortfolioExternalFlows(ctx context.Context, portfolioID string, dateFrom, dateBefore time.Time) ([]ExternalFlow, error)
	GetUserExternalFlows(ctx context.Context, userID string, dateFrom, dateBefore time.Time) ([]ExternalFlow, error)
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
	UserID       string
	JobType      string // one of the JobType* constants
	Broker       string // tx only
	Source       string // tx only
	Filename     string
	PeriodFrom   *timestamppb.Timestamp
	PeriodBefore *timestamppb.Timestamp
	Payload      []byte // serialized protobuf request
	// Parts are the archive parts a system archive job will apply. Their result
	// rows are written with the job so that a caller polling immediately sees a
	// row per part rather than an empty list it cannot distinguish from a job
	// that carried nothing.
	Parts []archivev1.ArchivePart
}

// JobPartResult is one archive part as applied by one job.
type JobPartResult struct {
	Part             archivev1.ArchivePart
	Status           apiv1.JobStatus
	TotalCount       int32
	ProcessedCount   int32
	Message          string // the hard failure that ended the part, if any
	ValidationErrors []*apiv1.ValidationError
}

// JobDetail is everything GetJob knows about one job. UserID is empty when no
// such job exists, which is how a caller distinguishes "not found" from an error.
type JobDetail struct {
	Status apiv1.JobStatus
	UserID string
	// Broker and Source are what the job was created against, and are empty for
	// an archive import, which names neither. They are read back rather than
	// taken from the payload so that a job whose payload will not load still
	// says whose upload it was.
	Broker               string
	Source               string
	TotalCount           int32
	ProcessedCount       int32
	ValidationErrors     []*apiv1.ValidationError // errors with no part of their own
	IdentificationErrors []IdentificationError
	Parts                []JobPartResult // in restore order; empty unless an archive import
}

// JobDB provides ingestion job operations.
type JobDB interface {
	CreateJob(ctx context.Context, params CreateJobParams) (string, error)
	GetJob(ctx context.Context, jobID string) (*JobDetail, error)
	SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error
	SetJobTotalCount(ctx context.Context, jobID string, total int32) error
	IncrJobProcessedCount(ctx context.Context, jobID string) error
	// SetJobProcessedCount sets a job's progress outright, for a job whose
	// progress is the sum over its parts rather than its own running count.
	SetJobProcessedCount(ctx context.Context, jobID string, processed int32) error
	// AppendValidationErrors attributes errors to one archive part, or to the
	// job itself when part is ARCHIVE_PART_UNSPECIFIED.
	AppendValidationErrors(ctx context.Context, jobID string, part archivev1.ArchivePart, errs []*apiv1.ValidationError) error
	AppendIdentificationErrors(ctx context.Context, jobID string, errs []IdentificationError) error
	SetJobPartStatus(ctx context.Context, jobID string, part archivev1.ArchivePart, status apiv1.JobStatus) error
	// SetJobPartFailed records the hard failure that ended a part and sets it FAILED.
	SetJobPartFailed(ctx context.Context, jobID string, part archivev1.ArchivePart, message string) error
	SetJobPartTotalCount(ctx context.Context, jobID string, part archivev1.ArchivePart, total int32) error
	// AddJobPartProcessedCount advances a part's progress by n. It takes a delta
	// rather than incrementing by one so that a caller can batch: a six-figure
	// price import that reported every row separately would spend more time
	// updating its own progress than importing.
	AddJobPartProcessedCount(ctx context.Context, jobID string, part archivev1.ArchivePart, n int32) error
	// ResetJobPartProgress zeroes a part's processed count, for a part being
	// re-run after the service restarted mid-job.
	ResetJobPartProgress(ctx context.Context, jobID string, part archivev1.ArchivePart) error
	LoadJobPayload(ctx context.Context, jobID string) ([]byte, error)
	ClearJobPayload(ctx context.Context, jobID string) error
	ListPendingJobs(ctx context.Context) ([]PendingJob, error)
	// ListJobs returns jobs for a user, newest first, with error counts. An empty
	// jobType matches every type. Returns (rows, totalCount, nextPageToken, error).
	ListJobs(ctx context.Context, userID, jobType string, pageSize int32, pageToken string) ([]JobRow, int32, string, error)
}

// IdentifierInput is a single (type, domain, value) for EnsureInstrument.
// Domain is empty or nil for broker-description and for identifiers that have no domain (e.g. ISIN, CUSIP).
// Canonical is false only for broker-description identifiers; true for standard identifiers (ISIN, CUSIP, etc.).
//
// ValidFrom and ValidBefore are the half-open interval in market time the name
// was correct for the instrument: ValidFrom is the vintage of the source that
// supplied it or the ex_date of the split that minted it, and a nil ValidBefore
// means it is the name the instrument wears now. Both are dates; a caller
// holding a timestamp truncates it. See
// docs/adr/0055-identifier-validity-is-an-interval.md.
type IdentifierInput struct {
	Type        string
	Domain      string // empty or NULL for no domain
	Value       string
	Canonical   bool // default true when not set for backward compat
	ValidFrom   *time.Time
	ValidBefore *time.Time
}

// VintageDate reduces a vintage to the date an identifier's validity is stated
// in. The bounds are dates (see docs/adr/0018-half-open-date-intervals.md), and
// what a vintage decides -- which side of an ex_date a name was stated on -- is
// a day rather than an instant. A nil vintage stays nil, meaning the name
// predates everything known about it.
func VintageDate(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := t.UTC().Truncate(24 * time.Hour)
	return &d
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

// InstrumentMerge is what an archive file says about an instrument, for filling
// in gaps in one that already exists. Every field is what the file carries; the
// merge never overwrites a value already stored.
type InstrumentMerge struct {
	AssetClass  string
	ExchangeMIC string
	Currency    string
	CIK         string
	SICCode     string
	ValidFrom   *time.Time
	ValidBefore *time.Time
	Identifiers []IdentifierInput
}

// OptionFields carries denormalized OCC components for option instruments.
// Nil when the instrument is not an option.
type OptionFields struct {
	Strike  decimal.Decimal
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

// PluginConfigWithCategory is a plugin_config row that names its own category,
// which PluginConfigRowFull does not: the admin API always knows the category it
// asked for, and an archive part does not.
type PluginConfigWithCategory struct {
	PluginID       string
	Category       string
	Enabled        bool
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

// ExportFetchBlock is one fetch block named the way a file names it: by the
// best identifier for the instrument rather than by its id. Both fetch-block
// tables export into this one shape, because they are the same statement about
// two fetchers.
type ExportFetchBlock struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	PluginID         string
	Reason           string
	FirstBlockedAt   time.Time
}

// FetchBlockInput is one fetch block to restore, carrying the knowledge time
// the file stated rather than letting the column default to now.
type FetchBlockInput struct {
	InstrumentID   string
	PluginID       string
	Reason         string
	FirstBlockedAt time.Time
}

// PriceFetchBlock records a permanently blocked (instrument, plugin) pair.
type PriceFetchBlock struct {
	InstrumentID string
	PluginID     string
	Reason       string
	// FirstBlockedAt is when the pair was first blocked. Never overwritten.
	FirstBlockedAt time.Time
}

// EODPriceRow is a single end-of-day price row for the admin price list.
type EODPriceRow struct {
	InstrumentID          string
	InstrumentDisplayName string
	PriceDate             time.Time
	Open                  *decimal.Decimal
	High                  *decimal.Decimal
	Low                   *decimal.Decimal
	Close                 decimal.Decimal
	AdjustedClose         *decimal.Decimal
	Volume                *int64
	DataProvider          string
	LastFetchedAt         time.Time
	ShareCountBasis       time.Time
}

// ExportPriceRow is a single price row with the best instrument identifier for export.
type ExportPriceRow struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	Currency         string
	PriceDate        time.Time
	// The share count this bar is denominated in, or nil when it is the bar's
	// own PriceDate. The column is NOT NULL and defaults to the price date, so
	// only a value that differs from it says anything, and only that value is
	// worth writing to a file.
	ShareCountBasis *time.Time
	Open            *decimal.Decimal
	High            *decimal.Decimal
	Low             *decimal.Decimal
	Close           decimal.Decimal
	AdjustedClose   *decimal.Decimal
	Volume          *int64
}

// ExportPriceCoverageRow is one half-open [From, Before) price coverage span
// with the best instrument identifier, and the instrument context an archive
// group needs.
//
// AssetClass and Currency travel with the span rather than being taken from the
// rows because an instrument that was covered and has no rows is still a group,
// and those two fields are what route the identifier plugins when the importing
// instance has never seen it.
type ExportPriceCoverageRow struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	Currency         string
	From             time.Time
	Before           time.Time
}

// ExportCoverageRow is one half-open [From, Before) corporate event coverage
// span with the best instrument identifier for export.
//
// AssetClass rides along because an instrument that was covered and has no
// events is still a group, and the asset class is what routes the identifier
// plugins when the importing instance does not know the instrument.
type ExportCoverageRow struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	AssetClass       string
	From             time.Time
	Before           time.Time
}

// EODPriceListDB provides paginated listing of EOD prices for admin UI.
type EODPriceListDB interface {
	// ListPrices returns EOD prices with optional search, half-open
	// [dateFrom, dateBefore) date range, and provider filters. A zero bound is
	// open-ended. Returns (rows, totalCount, nextPageToken, error).
	ListPrices(ctx context.Context, search string, dateFrom, dateBefore time.Time,
		dataProvider string, pageSize int32, pageToken string) ([]EODPriceRow, int32, string, error)
	// ListPricesForExport returns all EOD prices with the best identifier per instrument.
	// Instruments with no identifiers are excluded.
	ListPricesForExport(ctx context.Context) ([]ExportPriceRow, error)
	// ListPriceCoverageForExport returns the merged date spans from
	// price_coverage per instrument, with the best identifier and the
	// instrument's asset class and currency. A span with no rows inside it is
	// the only way an export can say a provider was asked and had nothing.
	ListPriceCoverageForExport(ctx context.Context) ([]ExportPriceCoverageRow, error)
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

// AssetClassToStr converts a proto AssetClass enum to its DB string. The enum
// value names are the stored vocabulary, so this is the identity apart from
// ASSET_CLASS_UNSPECIFIED, which maps to "".
func AssetClassToStr(ac typev1.AssetClass) string {
	if ac == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		return ""
	}
	return ac.String()
}

// BrokerToStr converts a proto Broker enum to the string the broker columns
// hold, which is the enum's own name. BROKER_UNSPECIFIED and any value this
// build does not know map to "", as AssetClassToStr does for its unspecified.
func BrokerToStr(b typev1.Broker) string {
	if _, ok := typev1.Broker_name[int32(b)]; !ok {
		return ""
	}
	if b == typev1.Broker_BROKER_UNSPECIFIED {
		return ""
	}
	return b.String()
}

// StrToBroker converts a stored broker string to its proto enum. An
// unrecognised string maps to BROKER_UNSPECIFIED.
func StrToBroker(s string) typev1.Broker {
	v, ok := typev1.Broker_value[s]
	if !ok {
		return typev1.Broker_BROKER_UNSPECIFIED
	}
	return typev1.Broker(v)
}

// TxTypeToStr returns the stored form of a tx type, which is its enum name.
// Unspecified is an error rather than an empty string: a posting has to say what
// kind of event it transcribes.
func TxTypeToStr(t typev1.TxType) (string, error) {
	if t == typev1.TxType_TX_TYPE_UNSPECIFIED {
		return "", fmt.Errorf("tx type unspecified")
	}
	s := t.String()
	if s == "TX_TYPE_UNSPECIFIED" {
		return "", fmt.Errorf("tx type unspecified")
	}
	return s, nil
}

// StrToTxType converts a stored tx type string to its proto enum. An
// unrecognised string maps to TX_TYPE_UNSPECIFIED.
func StrToTxType(s string) typev1.TxType {
	v, ok := typev1.TxType_value[s]
	if !ok {
		return typev1.TxType_TX_TYPE_UNSPECIFIED
	}
	return typev1.TxType(v)
}

// TxTypesToStrs returns the stored form of a declared tx type set. An empty set
// is an error for the reason a single unspecified type is: a posting has to say
// what kind of event it transcribes.
func TxTypesToStrs(ts []typev1.TxType) ([]string, error) {
	if len(ts) == 0 {
		return nil, fmt.Errorf("broker tx type set empty")
	}
	strs := make([]string, len(ts))
	for i, t := range ts {
		s, err := TxTypeToStr(t)
		if err != nil {
			return nil, err
		}
		strs[i] = s
	}
	return strs, nil
}

// StrsToTxTypes converts a stored tx type set to proto enums. An unrecognised
// string maps to TX_TYPE_UNSPECIFIED, as StrToTxType does.
func StrsToTxTypes(strs []string) []typev1.TxType {
	ts := make([]typev1.TxType, len(strs))
	for i, s := range strs {
		ts[i] = StrToTxType(s)
	}
	return ts
}

// accountTypePrefix is stripped from the enum name to get the stored form. The proto
// values are prefixed because enum values share package scope and TxType already
// defines INCOME and TRANSFER; the column stores the bare vocabulary the CHECK
// constraint and the specs use.
const accountTypePrefix = "ACCOUNT_TYPE_"

// AccountTypeToStr returns the stored form of an account type: its enum name without
// the prefix. Unspecified is USER rather than an error -- an upload that says nothing
// about a posting's kind is an ordinary broker account posting, which is what almost
// every row is. Derived from the generated names rather than mapped by hand so a new
// type cannot be stored under a spelling that StrToAccountType does not recognise.
func AccountTypeToStr(a typev1.AccountType) (string, error) {
	if a == typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED {
		return "USER", nil
	}
	s, ok := typev1.AccountType_name[int32(a)]
	if !ok {
		return "", fmt.Errorf("unknown account type: %v", a)
	}
	return strings.TrimPrefix(s, accountTypePrefix), nil
}

// StrToAccountType converts a stored account type string to its proto enum. An
// unrecognised string maps to ACCOUNT_TYPE_UNSPECIFIED.
func StrToAccountType(s string) typev1.AccountType {
	v, ok := typev1.AccountType_value[accountTypePrefix+s]
	if !ok {
		return typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED
	}
	return typev1.AccountType(v)
}

// scopePrefix and matchPrefix are stripped from the enum names to get the stored
// forms. The proto values are prefixed because Scope and Match both define an
// ACCOUNT and enum values share package scope; the columns store the bare
// vocabulary, as they do for AccountType.
const (
	scopePrefix = "SCOPE_"
	matchPrefix = "MATCH_"
)

// The stored forms of the Match vocabulary, for the passes that ask a correlation
// what may be done with it. Named rather than spelled at each site so a reader can
// find every consumer of one comparison.
const (
	MatchExact   = "EXACT"
	MatchOrdinal = "ORDINAL"
	MatchAccount = "ACCOUNT"
	// MatchAttaches is a reference to another posting's identifier rather than a
	// token this posting shares, so the pass consuming it compares the token
	// against other postings' tokens and concludes only that this posting joins
	// theirs. See docs/adr/0052-an-attaching-correlation-is-additive.md.
	MatchAttaches = "ATTACHES"
)

// The stored forms of the Scope vocabulary, for the passes that ask a correlation
// what set of postings its identifier means anything across. A scope is half of the
// statement a correlation makes and the match is the other half; neither is usable
// without the other, so they are named in the same place.
const (
	ScopeFile    = "FILE"
	ScopeAccount = "ACCOUNT"
	ScopeBroker  = "BROKER"
)

// Declares reports whether this correlation says the given comparison may be made
// of it. Nothing is inferred: a source with no numbering declares no ordinal rather
// than being discovered to have none, so a pass asks before it compares.
func (c Correlation) Declares(match string) bool {
	return slices.Contains(c.Match, match)
}

// ScopeToStr returns the stored form of a correlation scope: its enum name
// without the prefix. Unspecified is an error rather than a default, because a
// correlation that does not say what its identifier is comparable over cannot be
// compared at all, and guessing a scope invents evidence the source never gave.
func ScopeToStr(s typev1.Scope) (string, error) {
	name, ok := typev1.Scope_name[int32(s)]
	if !ok || s == typev1.Scope_SCOPE_UNSPECIFIED {
		return "", fmt.Errorf("unknown correlation scope: %v", s)
	}
	return strings.TrimPrefix(name, scopePrefix), nil
}

// StrToScope converts a stored scope string to its proto enum. An unrecognised
// string maps to SCOPE_UNSPECIFIED.
func StrToScope(s string) typev1.Scope {
	v, ok := typev1.Scope_value[scopePrefix+s]
	if !ok {
		return typev1.Scope_SCOPE_UNSPECIFIED
	}
	return typev1.Scope(v)
}

// MatchToStr returns the stored form of a correlation match: its enum name
// without the prefix. Unspecified is an error for the reason an unspecified
// scope is.
func MatchToStr(m typev1.Match) (string, error) {
	name, ok := typev1.Match_name[int32(m)]
	if !ok || m == typev1.Match_MATCH_UNSPECIFIED {
		return "", fmt.Errorf("unknown correlation match: %v", m)
	}
	return strings.TrimPrefix(name, matchPrefix), nil
}

// StrToMatch converts a stored match string to its proto enum. An unrecognised
// string maps to MATCH_UNSPECIFIED.
func StrToMatch(s string) typev1.Match {
	v, ok := typev1.Match_value[matchPrefix+s]
	if !ok {
		return typev1.Match_MATCH_UNSPECIFIED
	}
	return typev1.Match(v)
}

// PluginCategoryToStr converts a proto plugin category to the string
// plugin_config.category holds. It is the one shared vocabulary whose column
// spelling differs from its enum name, so the mapping lives here rather than
// being a String() call at each site.
func PluginCategoryToStr(c typev1.PluginCategory) string {
	switch c {
	case typev1.PluginCategory_IDENTIFIER:
		return PluginCategoryIdentifier
	case typev1.PluginCategory_DESCRIPTION:
		return PluginCategoryDescription
	case typev1.PluginCategory_PRICE:
		return PluginCategoryPrice
	case typev1.PluginCategory_INFLATION:
		return PluginCategoryInflation
	case typev1.PluginCategory_CORPORATE_EVENT:
		return PluginCategoryCorporateEvent
	default:
		return ""
	}
}

// StrToPluginCategory converts a plugin_config.category value to its proto
// enum. An unrecognised string maps to PLUGIN_CATEGORY_UNSPECIFIED.
func StrToPluginCategory(s string) typev1.PluginCategory {
	switch s {
	case PluginCategoryIdentifier:
		return typev1.PluginCategory_IDENTIFIER
	case PluginCategoryDescription:
		return typev1.PluginCategory_DESCRIPTION
	case PluginCategoryPrice:
		return typev1.PluginCategory_PRICE
	case PluginCategoryInflation:
		return typev1.PluginCategory_INFLATION
	case PluginCategoryCorporateEvent:
		return typev1.PluginCategory_CORPORATE_EVENT
	default:
		return typev1.PluginCategory_PLUGIN_CATEGORY_UNSPECIFIED
	}
}

// StrToAssetClass converts a DB asset class string to its proto enum. An
// unrecognised string maps to ASSET_CLASS_UNSPECIFIED.
func StrToAssetClass(s string) typev1.AssetClass {
	v, ok := typev1.AssetClass_value[s]
	if !ok {
		return typev1.AssetClass_ASSET_CLASS_UNSPECIFIED
	}
	return typev1.AssetClass(v)
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

// IsAssetClassCompatible reports whether a transaction whose stated
// asset_class_hint is `implied` may legitimately be linked to an instrument
// with asset class `resolved`.
//
// Rules:
//   - When `resolved` is empty or UNKNOWN (the instrument's class is unset
//     or the identifier plugin could not classify it) there is no signal to
//     contradict, so the tx is accepted.
//   - When `implied` is UNKNOWN (the source claims a security of unstated
//     class) any concrete class is accepted but CASH is rejected.
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

// currencyCodeRE is the shape users.display_currency holds: an ISO 4217 code.
var currencyCodeRE = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidCurrencyCode reports whether s is a well formed ISO 4217 code. Every
// path that writes a currency the user chose applies the same test, so it lives
// beside the column's other vocabularies rather than at one call site.
func ValidCurrencyCode(s string) bool {
	return currencyCodeRE.MatchString(s)
}

// InstrumentRow is a single instrument with its identifiers (for API responses).
// Nullable DB columns use pointer types; nil means NULL.
type InstrumentRow struct {
	ID                  string
	AssetClass          *string
	ExchangeMIC         *string
	Currency            *string
	Name                *string
	Exchange            string // denormalized; trigger-computed from acronym/identifier
	UnderlyingID        *string
	ValidFrom           *time.Time
	ValidBefore         *time.Time
	CIK                 *string
	SICCode             *string
	Strike              *decimal.Decimal // denormalized from OCC; NULL for non-options
	Expiry              *time.Time       // denormalized from OCC; NULL for non-options
	PutCall             *string          // "C" or "P"; NULL for non-options
	ContractMultiplier  decimal.Decimal  // deliverable multiplier; 1 = standard
	Identifiers         []IdentifierInput
	ProviderIdentifiers []ProviderIdentifierInput // provider-specific identifiers
	ExchangeName        *string                   // read-only; from exchanges JOIN
	ExchangeAcronym     *string                   // read-only; from exchanges JOIN
	ExchangeCountryCode *string                   // read-only; from exchanges JOIN
	// The underlying named by its highest-priority identifier rather than by
	// UUID, which is how a file has to name it. Populated by
	// ListInstrumentsForExport and nil everywhere else; use UnderlyingID within
	// one instance.
	UnderlyingIdentifierType   *string
	UnderlyingIdentifierValue  *string
	UnderlyingIdentifierDomain *string
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
	// ShareCountBasis is the date at which the share count DeclaredQty is
	// denominated in was current. Defaults to AsOfDate. See docs/spec/bitemporality.md.
	ShareCountBasis time.Time
	// Kind is derived, not stored: the earliest declaration for a holding is the
	// pad and the rest are assertions. Reads populate it; writes ignore it.
	Kind apiv1.DeclarationKind
	// Verified is what the transactions add up to at AsOfDate, computed on read.
	// Nil on the write paths, which return the stored row only.
	Verified *DeclarationCheck
}

// DeclarationCheck is a declaration measured against the holding it describes.
//
// It carries the two counts the comparison needs to bound its own rounding rather
// than a verdict, because the tolerance is a policy question -- how much
// disagreement is worth reporting -- and belongs with the handler that answers it,
// not with the query that measures.
type DeclarationCheck struct {
	// ComputedQty is the sum of the holding's USER postings up to and including
	// AsOfDate, converted into the declaration's ShareCountBasis. Includes the pad.
	ComputedQty decimal.Decimal
	// PostingCount is how many postings contributed.
	PostingCount int32
	// InexactBases is how many share count bases contributed a conversion that is
	// not 1/1 -- the only places the sum can carry a rounding. Zero means the
	// comparison against DeclaredQty is exact.
	InexactBases int32
}

// InitializeTx is the derived pad for a holding declaration: the synthetic posting
// that makes the declared quantity true, and the EQUITY counterparty that balances
// it. Timestamp is the portfolio start date and ShareCountBasis is the declaration's,
// which are independent -- the pad is dated when the history begins but denominated
// where the declaration is. The pad's tx type is not a field: value entering from
// outside the user's holdings is TRANSFER_EXTERNAL by definition, and the upsert
// writes that constant.
type InitializeTx struct {
	Timestamp       time.Time
	Quantity        decimal.Decimal
	ShareCountBasis time.Time
}

// ExportDeclaration is one stored declaration with the best identifier of the
// instrument it names, ready to be cut into statements.
//
// Neither the pad/assert discriminator nor the check against the computed
// holding is here. Both are derived from the declarations and the postings, and
// an archive carries inputs rather than what is derived from them. See
// docs/adr/0032-archive-preserves-inputs-not-derived-state.md.
type ExportDeclaration struct {
	Broker  string
	Account string
	// The instrument's best identifier, or all three empty for an instrument
	// carrying none. Chosen by bestIdentifierJoin so that every export naming
	// one identifier per instrument agrees which one.
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	DeclaredQty      decimal.Decimal
	AsOfDate         time.Time
	// The share count the declared quantity is denominated in, or nil when it is
	// the declaration's own as_of_date. The column is NOT NULL and the insert
	// trigger defaults it to that date, so only a value that differs from it
	// says anything worth writing to a file.
	ShareCountBasis *time.Time
}

// HoldingDeclarationDB provides holding declaration CRUD and INITIALIZE tx helpers.
type HoldingDeclarationDB interface {
	CreateHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time) (*HoldingDeclarationRow, error)
	UpdateHoldingDeclaration(ctx context.Context, id, declaredQty string, asOfDate, shareCountBasis time.Time) (*HoldingDeclarationRow, error)
	// UpsertHoldingDeclaration restates the declaration for a holding at a date,
	// or creates it where there is none. It is what an archive import writes
	// through: re-importing an exported file collides on the unique key at every
	// unchanged row, and the AlreadyExists the create path answers with -- right
	// for a user filling in a form -- would fail the restore.
	//
	// A zero shareCountBasis leaves the column NULL on insert so the table's
	// trigger applies the as_of_date default, keeping that rule in one place.
	UpsertHoldingDeclaration(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time) error
	DeleteHoldingDeclaration(ctx context.Context, id string) error
	GetHoldingDeclaration(ctx context.Context, id string) (*HoldingDeclarationRow, error)
	ListHoldingDeclarations(ctx context.Context, userID string) ([]*HoldingDeclarationRow, error)
	// ListHoldingDeclarationsForExport reads one user's declarations in archive
	// order -- by broker, then account, then date -- so a writer can cut them
	// into statements in one pass.
	ListHoldingDeclarationsForExport(ctx context.Context, userID string) ([]ExportDeclaration, error)
	// GetPortfolioStartDate returns the earliest real tx timestamp for the user, or nil if none exist.
	GetPortfolioStartDate(ctx context.Context, userID string) (*time.Time, error)
	// ComputeRunningBalance sums the real (non-synthetic) txs for the given holding
	// where timestamp >= from and timestamp < to, expressed in the share count
	// current at basis. Quantities are converted from each posting's own
	// share_count_basis, so the sum is in one denomination rather than a mixture.
	ComputeRunningBalance(ctx context.Context, userID, broker, account, instrumentID string, from, to, basis time.Time) (decimal.Decimal, error)
	// UpsertInitializeTx creates or updates the INITIALIZE synthetic tx for the given holding.
	UpsertInitializeTx(ctx context.Context, userID, broker, account, instrumentID string, init InitializeTx) error
	// DeleteInitializeTx deletes the INITIALIZE synthetic tx for the given holding, if it exists.
	DeleteInitializeTx(ctx context.Context, userID, broker, account, instrumentID string) error
	// CreateDeclarationWithInitializeTx atomically creates a declaration and upserts its INITIALIZE tx.
	CreateDeclarationWithInitializeTx(ctx context.Context, userID, broker, account, instrumentID, declaredQty string, asOfDate, shareCountBasis time.Time, init InitializeTx) (*HoldingDeclarationRow, error)
	// UpdateDeclarationWithInitializeTx atomically updates a declaration and upserts its INITIALIZE tx.
	UpdateDeclarationWithInitializeTx(ctx context.Context, id, declaredQty string, asOfDate, shareCountBasis time.Time, userID, broker, account, instrumentID string, init InitializeTx) (*HoldingDeclarationRow, error)
	// DeleteDeclarationWithInitializeTx atomically deletes a declaration and either
	// rewrites the holding's pad from init or, when init is nil, deletes it. A
	// deleted assertion leaves the pad alone; a deleted pad promotes the
	// next-earliest declaration to take its place.
	DeleteDeclarationWithInitializeTx(ctx context.Context, id, userID, broker, account, instrumentID string, init *InitializeTx) error
}

// InstrumentDB provides instrument resolution and plugin config.
type InstrumentDB interface {
	// EnsureInstrument finds an instrument by any of the given identifiers, or creates one with the given canonical fields and identifiers. Returns instrument ID. On unique violation (identifier already exists for another instrument), merges and returns the existing instrument ID. When assetClass is OPTION or FUTURE, underlyingID must be non-empty. exchangeMIC is the ISO 10383 MIC code (nullable). optionFields is non-nil only for OPTION instruments and supplies denormalized OCC components.
	EnsureInstrument(ctx context.Context, assetClass, exchangeMIC, currency, name, cik, sicCode string, identifiers []IdentifierInput, underlyingID string, validFrom, validBefore *time.Time, optionFields *OptionFields) (string, error)
	// FindInstrumentByIdentifier looks up instrument_id by (identifier_type, domain, value). Returns "" if not found. Use empty domain for no domain.
	FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error)
	// FindInstrumentWithMetaByIdentifier is like FindInstrumentByIdentifier but also returns asset_class, exchange_mic (ISO 10383 MIC code), and currency from the instruments table in one query.
	FindInstrumentWithMetaByIdentifier(ctx context.Context, identifierType, domain, value string) (instrumentID, assetClass, exchangeMIC, currency string, err error)
	// FindInstrumentByTypeAndValue looks up instrument_id by (identifier_type, value) with any domain. Returns "" if not found or if multiple instruments match (ambiguous).
	FindInstrumentByTypeAndValue(ctx context.Context, identifierType, value string) (string, error)
	// FindInstrumentByTickerIgnoringSeparators looks up instrument_id by a
	// MIC_TICKER value compared with split-ticker separators removed on both
	// sides. An OCC root spells a multi-class ticker without its separator --
	// BRKB for BRK.B -- and no substitution recovers one spelling from the
	// other, because nothing in BRKB says where the separator was. Returns ""
	// if not found or if several instruments match.
	FindInstrumentByTickerIgnoringSeparators(ctx context.Context, value string) (string, error)
	// FindInstrumentBySourceDescription looks up instrument_id by (source, NULL domain, instrument_description). Returns "" if not found.
	FindInstrumentBySourceDescription(ctx context.Context, source, description string) (string, error)
	// GetInstrument returns an instrument by ID with its identifiers, or nil if not found.
	GetInstrument(ctx context.Context, instrumentID string) (*InstrumentRow, error)
	// ListInstrumentsByIDs returns instruments by ID slice (for batch underlying lookup). Missing IDs are omitted; order not guaranteed.
	ListInstrumentsByIDs(ctx context.Context, ids []string) ([]*InstrumentRow, error)
	// ListInstrumentsForExport returns all instruments that have at least one
	// identifier with canonical = true, plus the underlying of every derivative
	// among them whether or not the filters would have selected it -- an archive
	// naming an underlying it does not carry is invalid. If assetClasses is
	// non-empty, filter to those classes; otherwise return every class,
	// including CASH, FX and the not-yet-classified, which is what a rebuild
	// needs. If exchangeFilter != "", filter by instruments.exchange_mic. Rows
	// carry every column a file needs, including the underlying's own identifier
	// triple. Order by instruments.id.
	ListInstrumentsForExport(ctx context.Context, exchangeFilter string, assetClasses []string) ([]*InstrumentRow, error)
	// ValidateMIC checks whether the given MIC code exists in the exchanges reference table.
	ValidateMIC(ctx context.Context, mic string) (bool, error)
	// ListInstruments returns instruments sorted alphabetically by display name (ticker, then name, then broker description). If search is non-empty, only instruments with at least one identifier value matching (case-insensitive substring) are returned. If assetClasses is non-empty, only instruments with matching asset_class are returned. Returns (rows, totalCount, nextPageToken, error).
	ListInstruments(ctx context.Context, search string, assetClasses []string, pageSize int32, pageToken string) ([]*InstrumentRow, int32, string, error)

	// InsertInstrumentIdentifier inserts a single identifier row.
	InsertInstrumentIdentifier(ctx context.Context, instrumentID string, input IdentifierInput) error
	// MergeInstrumentFromArchive fills in what an existing instrument does not
	// already have: identifiers it lacks, and columns that are still NULL. A
	// stored value always wins, so importing a file cannot rewrite reference
	// data the instance already had -- the seeded currency and FX rows above
	// all. Identifiers that conflict are skipped rather than failing the merge.
	MergeInstrumentFromArchive(ctx context.Context, instrumentID string, in InstrumentMerge) error
	// UpdateInstrumentStrike updates the strike on an existing option instrument.
	UpdateInstrumentStrike(ctx context.Context, instrumentID string, strike decimal.Decimal) error
	// SetContractMultiplier sets the deliverable multiplier on an existing
	// instrument. Separate from EnsureInstrument because the multiplier is not an
	// option term -- a future carries one too -- and because an import restoring
	// one is the only caller: a non-standard split leaves a fraction here and
	// nothing recomputes it, so a file that states it is the only way it comes
	// back. See docs/spec/corporate-events.md.
	SetContractMultiplier(ctx context.Context, instrumentID string, m decimal.Decimal) error
	// SaveProviderIdentifiers inserts provider-specific identifiers for an instrument.
	// Duplicates (same instrument, provider, type, domain, value) are silently ignored.
	SaveProviderIdentifiers(ctx context.Context, instrumentID string, ids []ProviderIdentifierInput) error
	// FindProviderIdentifiers returns provider-specific identifiers for an instrument and provider.
	FindProviderIdentifiers(ctx context.Context, instrumentID, provider string) ([]ProviderIdentifierInput, error)
	// LookupOperatingMIC returns the operating MIC for the given MIC code.
	// If mic is already an operating MIC it returns itself. Returns ("", error) if not found.
	LookupOperatingMIC(ctx context.Context, mic string) (string, error)
}

// InflationIndex is a single monthly inflation index value.
type InflationIndex struct {
	Currency     string
	Month        time.Time // 1st of month, UTC
	IndexValue   decimal.Decimal
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
	// On conflict (currency, month), overwrites with new data. fetchedAt is the
	// knowledge time to stamp on the rows; nil means now, which is what a fetch
	// wants and an import does not -- an imported row is only as fresh as the
	// file it came from.
	UpsertInflationIndices(ctx context.Context, indices []InflationIndex, fetchedAt *time.Time) error
	// ListInflationIndicesForExport returns every inflation index row, ordered
	// by currency then month, for the archive's inflation part. Unlike the price
	// and corporate event exports there is no coverage to join: a series is
	// dense and inflation_indices stores none.
	ListInflationIndicesForExport(ctx context.Context) ([]InflationIndex, error)
	// ListInflationIndices returns inflation data for admin UI listing with pagination.
	// currency is an optional filter (empty = all); the half-open
	// [dateFrom, dateBefore) range filters on month, and a nil bound is
	// open-ended. Returns (rows, nextPageToken, totalCount, error).
	ListInflationIndices(ctx context.Context, currency string, dateFrom, dateBefore *time.Time, pageSize int, pageToken string) ([]InflationIndex, string, int, error)
}

// StockSplit is a single stock split row. SplitFrom and SplitTo are the raw
// halves of the split ratio (factor = SplitTo / SplitFrom). They are stored as
// strings so that NUMERIC values from the database round-trip without loss of
// precision; conversion to float happens at the math boundary.
// PendingOptionSplits is one option contract together with the splits on its
// underlying that its stored identity does not yet reflect, ordered ascending by
// ex_date. Splits is never empty.
type PendingOptionSplits struct {
	Option *InstrumentRow
	Splits []StockSplit
}

type StockSplit struct {
	InstrumentID string
	ExDate       time.Time
	SplitFrom    string // numeric, e.g. "1"
	SplitTo      string // numeric, e.g. "2"
	DataProvider string
	// FirstKnownAt is when we first learned of this split. Honoured on insert
	// when set; on conflict it only ever moves backwards, so a re-import of an
	// older stamp restores it and a newer one is ignored. Zero means "stamp it
	// with the current time".
	FirstKnownAt time.Time
}

// CashDividend is a single cash dividend row. Amount is per share in Currency.
// PayDate, RecordDate, DeclarationDate, and Frequency are optional and may be
// nil/empty when the provider does not supply them. Type is "CD" (regular) or
// "SC" (special cash); defaults to "CD" when empty.
type CashDividend struct {
	InstrumentID    string
	ExDate          time.Time
	PayDate         *time.Time
	RecordDate      *time.Time
	DeclarationDate *time.Time
	Amount          string // numeric, e.g. "0.24"
	Currency        string
	Frequency       string // empty when unknown
	Type            string // "CD" or "SC"; empty = "CD"
	DataProvider    string
	// FirstKnownAt is when we first learned of this dividend. Honoured on
	// insert when set; on conflict it only ever moves backwards. Zero means
	// "stamp it with the current time".
	FirstKnownAt time.Time
}

// ExportUnhandledCorporateEvent is one unhandled event named the way a file
// names it: by the best identifier for the instrument rather than by its id.
type ExportUnhandledCorporateEvent struct {
	IdentifierType   string
	IdentifierValue  string
	IdentifierDomain string
	EventType        string
	ExDate           *time.Time
	Detail           string
	Data             []byte // JSONB, carried as the text it is stored as
	Resolved         bool
	CreatedAt        time.Time
}

// CorporateEventCoverage is one half-open [CoveredFrom, CoveredBefore) coverage
// interval for a (instrument, plugin). Adjacent or overlapping intervals for the
// same (InstrumentID, PluginID) are merged on insert by
// UpsertCorporateEventCoverage.
type CorporateEventCoverage struct {
	InstrumentID  string
	PluginID      string
	CoveredFrom   time.Time
	CoveredBefore time.Time
	LastFetchedAt time.Time
}

// CorporateEventFetchBlock records a permanently blocked (instrument, plugin)
// pair for corporate-event fetches. Mirrors PriceFetchBlock.
type CorporateEventFetchBlock struct {
	InstrumentID string
	PluginID     string
	Reason       string
	// FirstBlockedAt is when the pair was first blocked. Never overwritten.
	FirstBlockedAt time.Time
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

// OptionMint is one name a split gives an option contract, and the ex_date it
// became correct on. The strike is derived from the option's stored strike by
// the cumulative factor up to that split, so no rounded value ever feeds
// another (see docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md).
type OptionMint struct {
	ExDate time.Time
	OCC    IdentifierInput
	Strike decimal.Decimal
}

// OptionSplitParams bundles the mutations needed to restate a single option
// contract for the stock splits pending on its underlying.
type OptionSplitParams struct {
	InstrumentID string
	// OldOCCValue is the symbol the caller read. It is reported, not acted on:
	// every OCC identifier still in force is closed regardless, so the write
	// converges even if the stored symbol has moved on since the read.
	OldOCCValue string
	// Mints is one name per pending split, ordered by ex_date, and never empty.
	// Every name is written, not just the last: skipping the ones in between
	// would leave a window in which the contract has no recorded identity.
	Mints []OptionMint
}

// CorporateEventDB provides storage for stock splits, cash dividends, fetch
// coverage, fetch blocks, and the recompute primitive that derives the
// split_adjusted_* columns on eod_prices and txs from the raw values.
type CorporateEventDB interface {
	// UpsertStockSplits inserts or updates the supplied stock_splits rows.
	// On conflict (instrument_id, ex_date), all non-key columns are overwritten
	// except FirstKnownAt, which takes the earlier of the stored and supplied
	// values. A zero FirstKnownAt is stamped with the current time on insert.
	UpsertStockSplits(ctx context.Context, splits []StockSplit) error
	// ListStockSplits returns every stock split for the given instrument
	// ordered ascending by ex_date.
	ListStockSplits(ctx context.Context, instrumentID string) ([]StockSplit, error)
	// DeleteStockSplit removes a single (instrument, ex_date) row. Returns
	// nil even when no row exists; callers that need an "exists" signal should
	// check ListStockSplits first.
	DeleteStockSplit(ctx context.Context, instrumentID string, exDate time.Time) error

	// ListPendingOptionSplits returns every option whose OCC symbol in force
	// became correct before an effective split on its underlying, with the
	// splits that still need applying, ordered ascending by ex_date.
	// underlyingID == "" covers every option; a non-empty value restricts the
	// sweep to one underlying.
	//
	// This is the work list for the retroactive option split pass. It is derived
	// from that name's valid_from against ex_date rather than from which splits
	// happened to arrive in the current fetch cycle, so the pass is idempotent,
	// safe to run every cycle, and retries anything a previous run failed.
	ListPendingOptionSplits(ctx context.Context, underlyingID string) ([]PendingOptionSplits, error)

	// UpsertCashDividends inserts or updates the supplied cash_dividends rows.
	// On conflict (instrument_id, ex_date), all non-key columns are overwritten
	// except FirstKnownAt, which takes the earlier of the stored and supplied
	// values. A zero FirstKnownAt is stamped with the current time on insert.
	UpsertCashDividends(ctx context.Context, dividends []CashDividend) error
	// ListCashDividends returns every cash dividend for the given instrument
	// ordered ascending by ex_date.
	ListCashDividends(ctx context.Context, instrumentID string) ([]CashDividend, error)
	// DeleteCashDividend removes a single (instrument, ex_date) row.
	DeleteCashDividend(ctx context.Context, instrumentID string, exDate time.Time) error

	// UpsertCorporateEventCoverage records that (instrumentID, pluginID) has
	// been queried for the half-open interval [from, before), which must be
	// non-empty. Existing rows for the same (instrument, plugin) that are
	// adjacent or overlap with it are merged into a single row, which keeps the
	// oldest constituent LastFetchedAt. lastFetchedAt is when the supplied span
	// was confirmed; nil means now.
	UpsertCorporateEventCoverage(ctx context.Context, instrumentID, pluginID string, from, before time.Time, lastFetchedAt *time.Time) error
	// ListCorporateEventCoverage returns coverage rows for the given
	// instruments. When instrumentIDs is empty all coverage rows are returned.
	// Rows are sorted by (instrument_id, plugin_id, covered_from).
	ListCorporateEventCoverage(ctx context.Context, instrumentIDs []string) ([]CorporateEventCoverage, error)
	// ListCorporateEventCoverageForExport returns coverage spans per instrument
	// with the best identifier, merged across plugins. An import records every
	// span as data_provider = "import", so the per-plugin split does not
	// survive a round trip.
	ListCorporateEventCoverageForExport(ctx context.Context) ([]ExportCoverageRow, error)

	// CreateCorporateEventFetchBlock blocks (instrument, plugin) for future
	// corporate-event fetch attempts. Idempotent on (instrument_id, plugin_id).
	CreateCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error
	// DeleteCorporateEventFetchBlock removes a block; nil when no row exists.
	DeleteCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID string) error
	// ListCorporateEventFetchBlocks returns every block row.
	ListCorporateEventFetchBlocks(ctx context.Context) ([]CorporateEventFetchBlock, error)

	// ListCorporateEventFetchBlocksForExport mirrors
	// ListPriceFetchBlocksForExport for the corporate event fetcher.
	ListCorporateEventFetchBlocksForExport(ctx context.Context) ([]ExportFetchBlock, error)
	// UpsertCorporateEventFetchBlocks mirrors UpsertPriceFetchBlocks.
	UpsertCorporateEventFetchBlocks(ctx context.Context, blocks []FetchBlockInput) error
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

	// ApplyOptionSplit atomically restates an option contract for the stock
	// splits pending on its underlying: closes the OCC symbol still in force at
	// the first ex_date, mints one row per split from that ex_date, updates the
	// strike, and recomputes split-adjusted tx values. The name is left to the
	// trigger, which derives it from the identifier still in force. All
	// mutations run in a single transaction so partial failure cannot leave the
	// option inconsistent. No derived split row is written on the option --
	// split_factor_at resolves splits through the underlying_id FK.
	//
	// A minted name another instrument already holds absorbs that instrument
	// rather than failing: it is a duplicate created while the split was
	// unknown, and rolling back would leave this option pending forever.
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

	// ListUnhandledCorporateEventsForExport returns every unhandled event,
	// resolved and unresolved alike, with the best identifier per instrument.
	// Unpaged, because a file carries the lot.
	ListUnhandledCorporateEventsForExport(ctx context.Context) ([]ExportUnhandledCorporateEvent, error)
	// RestoreUnhandledCorporateEvents writes events from an archive, honouring
	// their resolved flag and detection time, and returns how many were
	// inserted. A row matching an already-stored (instrument_id, event_type,
	// ex_date, resolved) is skipped, so importing the same file twice does not
	// double the review queue.
	RestoreUnhandledCorporateEvents(ctx context.Context, events []UnhandledCorporateEvent) (int, error)
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
	FirstKnownAt     time.Time
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
	Type             string
	FirstKnownAt     time.Time
}

// ResidualBalance is the net value left in one non-asset account, aggregated over
// every user: what events of one tx type left in one broker account in one
// commodity. An IMBALANCE balance is a group that did not sum to zero -- a missing
// fee or an uncategorised dividend; a TRANSFER_CLEARING balance is one side of a
// journal, whether or not the other side has arrived.
//
// It carries no user identity: the report measures how lossy each broker converter
// is, not what is in any one portfolio.
type ResidualBalance struct {
	AccountType  typev1.AccountType
	Broker       typev1.Broker
	Account      string
	InstrumentID string
	// Commodity is the instrument's name: the ISO code for money, the ticker for a
	// security. AssetClass says which, and is empty when the instrument was never
	// identified.
	Commodity      string
	AssetClass     string
	ResolvedTxType typev1.TxType
	Balance        decimal.Decimal
	PostingCount   int32
	// Oldest and Newest bound the postings that contribute to the balance. For a
	// transfer that is the age of a missing side: a matched pair is excluded from
	// the report, so what remains is a side whose counterpart never arrived.
	Oldest *time.Time
	Newest *time.Time
}

// ResidualBalanceOpts filters the residual balance report. From and Before are a
// half-open [From, Before) window over the posting order_date; nil bounds are
// open-ended. An AccountType of ACCOUNT_TYPE_UNSPECIFIED returns both residual
// types.
type ResidualBalanceOpts struct {
	From        *time.Time
	Before      *time.Time
	AccountType typev1.AccountType
}

// ResidualBalanceDB aggregates the residual postings -- the IMBALANCE and
// TRANSFER_CLEARING legs routed to balance groups their source data left one-sided
// -- across all users.
type ResidualBalanceDB interface {
	ListResidualBalances(ctx context.Context, opts ResidualBalanceOpts) ([]ResidualBalance, error)
	// CountResidualBalances returns the number of non-zero IMBALANCE balances and
	// the number of unmatched TRANSFER_CLEARING balances older than staleBefore,
	// over all of history. A matched pair is settled and is not counted.
	CountResidualBalances(ctx context.Context, staleBefore time.Time) (imbalances, staleTransfers int32, err error)
}

// Match methods for the transfer_matches table, mirroring its CHECK constraint.
// There is no amount-and-window-alone method: a pair with no evidence beyond an
// equal and opposite amount is left unmatched rather than guessed at.
const (
	// TransferMatchPointer -- the source named the other account outright.
	TransferMatchPointer = "POINTER"
	// TransferMatchReference -- the two sides' broker references are adjacent.
	TransferMatchReference = "REFERENCE"
	// TransferMatchManual -- a person said so. Nothing produces one yet; the
	// matcher only ever inserts, so one will survive every rebuild.
	TransferMatchManual = "MANUAL"
)

// TransferSide is one unmatched side of a transfer: the TRANSFER_CLEARING residual
// of a journal group, carrying the evidence the rest of its group holds. One row per
// (group, commodity), because balancing emits one residual per commodity and that
// pair is what a match is keyed on.
type TransferSide struct {
	UserID       string
	GroupID      string
	Broker       typev1.Broker
	Account      string
	InstrumentID string
	// Amount is the residual's split-adjusted quantity, signed. Positive means the
	// value left this account -- the group's own leg is negative and the clearing
	// leg holds what is owed out -- and negative means it arrived. A pair is two
	// sides summing to exactly zero.
	Amount    decimal.Decimal
	OrderDate time.Time
	// Correlations is what the group's sources said about why its postings might
	// belong with others: their own identifiers, and what may be compared about
	// each. A set over the whole group rather than one value, because a group is
	// several source rows, the evidence can sit on any of them, and the nearest
	// reference over the whole set is what the sample data supports.
	//
	// Which pass can read a given correlation is the correlation's own to say:
	// the reference pass takes those declaring MATCH_ORDINAL and compares their
	// ordinals, the pointer pass those declaring MATCH_ACCOUNT and compares their
	// tokens against the other side's account. An OFX FITID declares neither, so
	// both passes pass over it, which is right -- it is opaque and unique within
	// one account.
	Correlations []Correlation
}

// TransferMatch links the two sides of one transfer in one commodity. FromGroupID is
// the side the value left and ToGroupID where it arrived.
type TransferMatch struct {
	UserID       string
	FromGroupID  string
	ToGroupID    string
	InstrumentID string
	Method       string
}

// TransferSideOpts filters the unmatched sides read for matching. An empty UserID
// reads every user's, which is what a whole-corpus pass wants.
type TransferSideOpts struct {
	UserID string
}

// TransferMatchDB records which tx group holds the other side of a transfer.
//
// The links are derived and disposable: they cascade on group delete, so a re-upload
// leaves the surviving side unmatched and the matcher rebuilds it. Nothing here
// updates or deletes a link, which is what lets a MANUAL match survive a rebuild.
type TransferMatchDB interface {
	// ListUnmatchedTransferSides returns every TRANSFER_CLEARING residual that no
	// match names, with the evidence its group carries. Ordered by
	// (user_id, instrument_id, timestamp, group_id) so a matching pass is
	// deterministic.
	ListUnmatchedTransferSides(ctx context.Context, opts TransferSideOpts) ([]TransferSide, error)
	// CreateTransferMatches inserts links and returns how many were written. It
	// skips any link whose either side is already matched in that commodity, so a
	// re-run over unchanged data writes nothing.
	CreateTransferMatches(ctx context.Context, matches []TransferMatch) (int, error)
	// ListTransferMatches returns one user's links, newest first. For the report
	// and for tests; the matching path itself reads none.
	ListTransferMatches(ctx context.Context, userID string) ([]TransferMatch, error)
}

// GroupingSeedOpts chooses where a grouping cycle starts looking.
//
// Two sources, because one cannot find what the other can. Residual picks the groups
// carrying something a missing leg would explain, which is most of what needs
// repairing but never a converter's wrong pairing of two similar trades -- that
// balances with only a rounding residual and looks settled from outside. JobID picks
// what an import just wrote, whatever state its groups are in. An empty JobID with
// Residual false reads everything the user has, which is the full partition the
// worst case degenerates to anyway.
type GroupingSeedOpts struct {
	UserID string
	// Residual seeds from groups holding a residual worse than SOURCE_ROUNDING.
	Residual bool
	// JobID seeds from the postings one ingestion job wrote.
	JobID string
}

// The reads a grouping rule may make while growing the neighbourhood it is
// partitioned over. One query type per access path, each carrying only its own
// fields.
//
// A rule can express no reach these do not offer, which is the point: docs/adr/
// 0050-grouping-recomputes-a-neighbourhood.md makes "state your reach as a bounded
// indexed query" an admissibility test, and a rule limited to calling these cannot
// state an unindexed one. A new access path is a new method here and a new statement
// behind it, which is what a new access path honestly is.
type (
	// TokenQuery asks who else holds an identifier, in one series. AnyAccount
	// widens it to the broker, for a token whose scope says it means something
	// outside the account that issued it.
	TokenQuery struct {
		Broker     typev1.Broker
		Account    string
		AnyAccount bool
		Label      string
		Token      string
	}

	// DateQuery asks for one account's postings over a span of time, half-open as
	// every interval in this system is
	// (docs/adr/0018-half-open-date-intervals.md).
	DateQuery struct {
		Broker  typev1.Broker
		Account string
		From    time.Time
		Before  time.Time
	}

	// OrdinalQuery asks for one account's postings whose reference falls in a
	// span, inclusive at both ends because a span is stated as a distance rather
	// than as a range.
	OrdinalQuery struct {
		Broker  typev1.Broker
		Account string
		Label   string
		Low     int64
		High    int64
	}
)

// GroupingReader answers the reads above.
//
// Every method takes a batch and the ids the caller already holds, so a round of the
// closure costs one statement per access path rather than one per posting asking, and
// a posting is never read twice.
type GroupingReader interface {
	PostingsByToken(ctx context.Context, userID string, qs []TokenQuery, held []string) ([]GroupingPosting, error)
	PostingsByDates(ctx context.Context, userID string, qs []DateQuery, held []string) ([]GroupingPosting, error)
	PostingsByOrdinals(ctx context.Context, userID string, qs []OrdinalQuery, held []string) ([]GroupingPosting, error)
}

// Settler decides which postings are legs of one event, given somewhere to start and
// a reader to grow the region from, and returns only where it disagrees with what is
// stored.
//
// The store calls it while writing, so that an upload's postings are partitioned in
// the transaction that inserts them and no group is ever observed in whatever shape
// the postings happened to arrive in. The reader it is handed reads that same
// transaction, which is how the engine sees both the postings just written and the
// ones already stored beside them.
//
// It is an interface here and implemented in server/grouping because the rules that
// decide a partition are not the store's business, and because the store cannot
// import the package that reads it. A store with no settler stores what it is given
// and groups nothing, which is what the tests that are not about grouping want.
type Settler interface {
	Settle(ctx context.Context, userID string, seed []GroupingPosting, r GroupingReader) ([]GroupChange, error)
}

// GroupChange is one group the engine drew that the stored partition does not have,
// and what it concluded about each of its members.
//
// Only disagreements are carried. A group whose membership is already stored produces
// no change at all, which is what keeps its id -- and the transfer matches keyed on
// that id -- through a cycle that repartitioned nothing.
type GroupChange struct {
	Members []GroupMemberChange
}

// GroupMemberChange is one posting of a changed group.
//
// Moving separates the two things a regroup does to a posting. A member that is
// moving joins the new group; one that is not is already where the engine wants it
// and is only being retyped, because the resolved value is derived from the partition
// and can change while the membership does not.
type GroupMemberChange struct {
	ID string
	// FromGroupID is the group the posting is leaving, so its residuals can be
	// routed again. Empty for a member that is not moving.
	FromGroupID string
	// Resolved is the stored spelling of what the claiming rule concluded.
	Resolved string
	Moving   bool
}

// GroupingDB is everything a grouping cycle reads.
//
// Reads only. The partition is computed in memory and written by a separate call, so
// a cycle can run in shadow -- deriving the groups and comparing them against what is
// stored -- with no path through here writing anything.
type GroupingDB interface {
	GroupingReader
	// ListGroupingSeeds returns the transcribed postings a cycle starts from,
	// ordered by id so a cycle is deterministic.
	ListGroupingSeeds(ctx context.Context, opts GroupingSeedOpts) ([]GroupingPosting, error)
	// ApplyGrouping writes the groups the engine drew that are not already stored,
	// re-routing the residuals of every group it touched, and returns how many
	// postings moved. All in one transaction: the deferred balance constraint is
	// what lets a group be observed unbalanced between the move and the routing.
	ApplyGrouping(ctx context.Context, userID string, changes []GroupChange) (int, error)
}

// GroupingPosting is one transcribed posting as the grouping engine reads it: the
// fields its rules compare, the evidence its source supplied, and the group it
// currently sits in.
//
// Routed residuals are not among them. They transcribe nothing and correlate with
// nothing, so there is nothing to say which postings they belong with; they are
// deleted and routed fresh once the partition is settled, rather than partitioned
// (docs/adr/0043-grouping-does-not-travel-in-the-archive.md).
type GroupingPosting struct {
	ID      string
	UserID  string
	Broker  typev1.Broker
	Account string
	// OrderDate is what the rules that bucket on a day bucket on. A group's
	// postings need not share it -- a deposit run in the sample settles across two
	// days -- so only the rules that say so use it.
	//
	// The order date rather than the trade date, because that is the one a trade
	// and the charges levied on it agree about: a broker dates a charge by when it
	// cleared and a trade by when it settled, and those are days apart.
	OrderDate time.Time
	// InstrumentID is the commodity, empty for a posting whose instrument never
	// resolved.
	InstrumentID string
	// Quantity is signed, and its sign is what the trade rules read as the
	// direction money moved: the broker row type that says so in the source is
	// not carried, and the sign is what survives of it.
	Quantity  decimal.Decimal
	UnitPrice *decimal.Decimal
	// SettlementAmount is the cash total the source stated for the row, absent on
	// a posting whose quantity is already money. Independent of
	// quantity * unit_price, which is what lets one rule identify a cash leg and
	// the other check the identification.
	SettlementAmount *decimal.Decimal
	// Declared is the candidate type set the source stated. A rule may claim a
	// posting only where this admits the rule's own type, which is 0044's *may be*
	// predicate doing the work.
	Declared []typev1.TxType
	// JobID is the ingestion job that wrote the posting, and is what a SCOPE_FILE
	// correlation is comparable within.
	JobID string
	// GroupID is the partition as it stands, and Resolved what the last cycle
	// concluded about this posting. The engine reads both only to tell its own
	// answer from what is there, never as evidence.
	GroupID      string
	Resolved     string
	Correlations []Correlation
}

// Telemetry.
//
// Run-scoped event rows in the telemetry schema, replacing the Redis counters.
// See docs/spec/telemetry.md for the grains and their vocabularies, and
// docs/adr/0053-telemetry-is-run-scoped-event-rows.md for why.

// TelemetryRetention is how long a run and its event rows are kept. Traffic is
// low and a problem may go unnoticed for a long time, so the window is set to
// outlast the gap between a regression landing and someone looking for it.
const TelemetryRetention = 360 * 24 * time.Hour

// Run kinds. One activation of one subsystem, of which an ingestion job and a
// worker cycle are two kinds.
const (
	TelemetryRunTxImport            = "tx_import"
	TelemetryRunUserArchiveImport   = "user_archive_import"
	TelemetryRunSystemArchiveImport = "system_archive_import"
	TelemetryRunGroupingCycle       = "grouping_cycle"
	TelemetryRunTransferMatchCycle  = "transfer_match_cycle"
	TelemetryRunCorporateEventCycle = "corporate_event_cycle"
	TelemetryRunPriceFetchCycle     = "price_fetch_cycle"
	TelemetryRunInflationCycle      = "inflation_cycle"
)

// Run outcomes. TelemetryOutcomeIncomplete means the run died, and is stamped by
// a sweep over runs left without a terminal outcome, which is what lets an
// unstamped run mean genuinely in flight.
const (
	TelemetryOutcomeSuccess    = "success"
	TelemetryOutcomeFailed     = "failed"
	TelemetryOutcomeIncomplete = "incomplete"
)

// Extraction outcomes, stage 1 of a resolution key. The not_attempted members are
// where the skips live: extraction is skipped for a description already resolved
// by DB lookup, and for one whose every posting names an identifier, because
// extraction exists to find an identifier and is a paid call.
const (
	TelemetryExtractionHintsFound                = "hints_found"
	TelemetryExtractionNoHints                   = "no_hints"
	TelemetryExtractionNotAttemptedDBHit         = "not_attempted_db_hit"
	TelemetryExtractionNotAttemptedHintsSupplied = "not_attempted_hints_supplied"
	TelemetryExtractionNotAttemptedTypeFilter    = "not_attempted_type_filter"
	TelemetryExtractionNotAttemptedNoPlugins     = "not_attempted_no_plugins"
)

// Resolution outcomes, stage 2 of a resolution key. The two db_ members are
// distinct lookups -- by stored (source, description) and by supplied identifier
// hints -- and conflating them hides which path is carrying an import.
const (
	TelemetryResolutionDBSourceDescription   = "db_source_description"
	TelemetryResolutionDBIdentifierHints     = "db_identifier_hints"
	TelemetryResolutionIdentified            = "identified"
	TelemetryResolutionBrokerDescriptionOnly = "broker_description_only"
	TelemetryResolutionExtractionFailed      = "extraction_failed"
	TelemetryResolutionPluginTimeout         = "plugin_timeout"
	TelemetryResolutionPluginUnavailable     = "plugin_unavailable"
	TelemetryResolutionConflictingHints      = "conflicting_hints"
)

// Identification attempt purposes. A single resolution key produces several
// attempts: one primary, two more when the mismatch check runs, and one per level
// of underlying recursion.
const (
	TelemetryPurposePrimary       = "primary"
	TelemetryPurposeMismatchCheck = "mismatch_check"
	TelemetryPurposeUnderlying    = "underlying"
)

// Identification attempt outcomes. A plugin filtered out by acceptable kind or
// security type produces no plugin-call row, because no call was made; when that
// filter removes every plugin the attempt records no_eligible_plugins.
const (
	TelemetryAttemptDBShortCircuit    = "db_short_circuit"
	TelemetryAttemptNoEligiblePlugins = "no_eligible_plugins"
	TelemetryAttemptIdentified        = "identified"
	TelemetryAttemptNotIdentified     = "not_identified"
	TelemetryAttemptPluginTimeout     = "plugin_timeout"
	TelemetryAttemptPluginError       = "plugin_error"
)

// Identifier plugin call outcomes the orchestrator composes. The rest of the
// vocabulary is the plugin's own transport outcome and lives on
// identifier.Outcome, which a caller spells with string(). These three are all
// successes and are decided after every plugin has returned: superseded lost to a
// better hint match despite higher precedence, and discarded_inconsistent was
// dropped as contradicting the winner. No plugin can know either.
const (
	TelemetryPluginCallWon                   = "won"
	TelemetryPluginCallSuperseded            = "superseded"
	TelemetryPluginCallDiscardedInconsistent = "discarded_inconsistent"
)

// Price gap outcomes. TelemetryGapSettledEmpty is a success and the mirror of
// broker_description_only: no prices were stored and the gap is nonetheless
// finished with, because the instrument did not trade over the range or no plugin
// reaches that far back. Reading it as a failure would put every untraded week
// into a panel meant to show outages.
const (
	TelemetryGapFilled            = "filled"
	TelemetryGapSettledEmpty      = "settled_empty"
	TelemetryGapNoEligiblePlugin  = "no_eligible_plugin"
	TelemetryGapAllPluginsFailed  = "all_plugins_failed"
	TelemetryGapInstrumentMissing = "instrument_missing"
)

// Price plugin call outcomes. TelemetryPriceCallNoData is an answer rather than a
// failure to answer and settles the range for that plugin;
// TelemetryPriceCallHistoryLimit made no call at all, the plugin's configured
// reach not extending that far back; and TelemetryPriceCallUpsertFailed is our
// database rather than the provider's API.
const (
	TelemetryPriceCallBarsReturned   = "bars_returned"
	TelemetryPriceCallNoData         = "no_data"
	TelemetryPriceCallHistoryLimit   = "history_limit"
	TelemetryPriceCallPermanentBlock = "permanent_block"
	TelemetryPriceCallTimeout        = "timeout"
	TelemetryPriceCallError          = "error"
	TelemetryPriceCallUpsertFailed   = "upsert_failed"
)

// TelemetryRun is one activation of one subsystem, as it stands when it starts.
// JobID is empty for a cycle, as are UserID, Broker and Source, which cycles run
// without.
type TelemetryRun struct {
	Kind   string
	JobID  string
	UserID string
	Broker string
	Source string
}

// TelemetryResolutionKey is one distinct (source, description, identifier hints)
// triple within a run, as it stands before it resolves. Not one transaction: many
// transactions share a key and resolve once, and TxCount records that fan-out so a
// failure affecting 300 rows can be told from one affecting 1.
type TelemetryResolutionKey struct {
	RunID              string
	Source             string
	Description        string
	TxCount            int
	HadIdentifierHints bool
	SecurityTypeHint   string
	InstrumentKind     string
}

// TelemetryResolutionKeyOutcome is what became of a resolution key, stamped onto
// the row when it resolves. InstrumentID is empty when it did not.
type TelemetryResolutionKeyOutcome struct {
	RunID             string
	ExtractionOutcome string
	Outcome           string
	MismatchDetected  bool
	// HintDiffs summarises what the resolved instrument contradicted about its
	// hints, empty when it contradicted nothing.
	HintDiffs    string
	InstrumentID string
}

// TelemetryIdentificationAttempt is one ResolveWithPlugins call, written when it
// finishes. RunID is not stored on the row -- an attempt reaches its run through
// its resolution key -- and is carried only so a failed write can mark the run.
type TelemetryIdentificationAttempt struct {
	RunID              string
	ResolutionKeyID    string
	Purpose            string
	Depth              int
	Outcome            string
	SecurityTypeHint   string
	AssetClass         string
	HadIdentifierHints bool
}

// TelemetryIdentifierPluginCall is one plugin invocation within an attempt.
// Outcome is the orchestrator's composition of the plugin's transport outcome with
// what it decided afterwards; Retries and Duration are the orchestrator's alone,
// since the retry loop and the clock belong to it. RunID is carried for the same
// reason as on the attempt.
type TelemetryIdentifierPluginCall struct {
	RunID     string
	AttemptID string
	PluginID  string
	Outcome   string
	Retries   int
	Duration  time.Duration
}

// TelemetryTokens is the token cost of one description plugin call. Nil on a call
// to a plugin that costs no tokens, which is what keeps the columns null rather
// than zero for it.
type TelemetryTokens struct {
	Prompt     int64
	Completion int64
	Total      int64
}

// TelemetryDescriptionPluginCall is one plugin invocation over a batch. It hangs
// off the run rather than off a resolution key: one ExtractBatch call covers many
// descriptions at once, so it has no single parent key.
//
// BatchSize is a different population per plugin -- description plugins run in
// precedence order and each sees only the items its predecessors failed on -- so
// rates are not comparable between them. Precedence is what makes that order
// readable afterwards, higher first.
type TelemetryDescriptionPluginCall struct {
	RunID          string
	PluginID       string
	Precedence     int
	BatchSize      int
	ItemsWithHints int
	Outcome        string
	Tokens         *TelemetryTokens
	Duration       time.Duration
}

// TelemetryPriceGap is one instrument a price fetch cycle set out to fill, as it
// stands before any plugin is put to it. Not one price row and not one provider
// call: an instrument's outstanding history is put to several plugins over several
// ranges, and DaysOutstanding records the size of that ask so a cycle that got
// slower can be told from one that was asked for more.
//
// AssetClass, Currency and Exchange are the three fields plugin filtering reads.
// They are recorded rather than looked up later because they are the inputs to the
// decision the row explains, and any of them may be empty on an instrument that
// carries none -- which is itself why a plugin accepted it.
type TelemetryPriceGap struct {
	RunID           string
	InstrumentID    string
	IsFX            bool
	AssetClass      string
	Currency        string
	Exchange        string
	DaysOutstanding int
}

// TelemetryPricePluginCall is one outstanding range put to one plugin, which is
// one FetchPrices call in every case but history_limit.
//
// The range rather than the plugin is the grain: one plugin is asked separately
// for each range a gap leaves outstanding, and those calls can end differently.
// Precedence is the plugin's position in the configured order, which is what makes
// a skipped plugin readable as a gap in the sequence.
//
// RunID is not stored on the row -- a call reaches its run through its gap -- and
// is carried only so a failed write can mark the run.
type TelemetryPricePluginCall struct {
	RunID      string
	GapID      string
	PluginID   string
	Precedence int
	// Half-open [From, Before), as the orchestrator's ranges are. The span in days
	// is derived by the view rather than carried here.
	From    time.Time
	Before  time.Time
	Bars    int
	Outcome string
	// Duration is nil for history_limit, which called nothing and so has no clock.
	Duration *time.Duration
}

// TelemetryDB writes the event rows in the telemetry schema.
//
// It is deliberately not part of DB. It holds its own connection pool and never
// joins the work's transaction: a failed import rolls back, and telemetry riding
// along would erase the diagnostics for the run most worth inspecting.
//
// No write method returns an error, because telemetry never fails the work. A
// write that fails is logged, sets telemetry_incomplete on its run and yields an
// empty id; a write whose parent id is empty is skipped, so one failure costs its
// own subtree and nothing else. A caller therefore threads ids through without
// testing them.
type TelemetryDB interface {
	// StartRun creates a run and returns its id. The run is stamped separately,
	// when it ends, which is what leaves a run whose process died unstamped.
	StartRun(ctx context.Context, r TelemetryRun) string
	// EndRun stamps ended_at and a terminal outcome.
	EndRun(ctx context.Context, runID, outcome string)

	// StartResolutionKey creates a resolution key and returns its id. Like a run
	// it is created and later stamped, because its identification attempts
	// reference it and so it must exist before its own outcome is known.
	StartResolutionKey(ctx context.Context, k TelemetryResolutionKey) string
	// EndResolutionKey stamps what became of a resolution key.
	EndResolutionKey(ctx context.Context, keyID string, o TelemetryResolutionKeyOutcome)

	// WriteIdentificationAttempt records a finished attempt and returns its id,
	// for the plugin calls written under it.
	WriteIdentificationAttempt(ctx context.Context, a TelemetryIdentificationAttempt) string
	WriteIdentifierPluginCall(ctx context.Context, c TelemetryIdentifierPluginCall)
	WriteDescriptionPluginCall(ctx context.Context, c TelemetryDescriptionPluginCall)

	// StartPriceGap creates a price gap and returns its id, for the reason
	// StartResolutionKey does: its plugin calls reference it, so it must exist
	// before its own outcome is known.
	StartPriceGap(ctx context.Context, g TelemetryPriceGap) string
	// EndPriceGap stamps what became of a gap. A gap left unstamped is a cycle
	// that died before reaching it, which is what makes where it stopped readable.
	EndPriceGap(ctx context.Context, runID, gapID, outcome string)
	WritePricePluginCall(ctx context.Context, c TelemetryPricePluginCall)

	// SweepIncompleteRuns stamps every run left without a terminal outcome as
	// incomplete, and returns how many it stamped. Called once at startup, before
	// any work begins: a run with no outcome is then either one this process is
	// running now or one whose process died, and the sweep is what tells the two
	// apart. It returns an error for the reason PurgeRunsBefore does.
	SweepIncompleteRuns(ctx context.Context) (int64, error)

	// PurgeRunsBefore deletes runs started before cutoff, cascading to their event
	// rows, and returns how many runs went. Unlike the writes it returns an error,
	// because it is the work its caller was asked to do rather than telemetry
	// alongside other work.
	PurgeRunsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// NopTelemetry is a TelemetryDB that records nothing, for a build or a test with
// no telemetry pool behind it.
//
// It exists so that no code below the wiring point tests a handle for nil. Its
// StartRun yields an empty id, and the writer contract already skips a row whose
// parent id is empty, so an entire subtree of calls costs one comparison each and
// writes nothing.
type NopTelemetry struct{}

var _ TelemetryDB = NopTelemetry{}

func (NopTelemetry) StartRun(context.Context, TelemetryRun) string { return "" }

func (NopTelemetry) EndRun(context.Context, string, string) {}

func (NopTelemetry) StartResolutionKey(context.Context, TelemetryResolutionKey) string { return "" }

func (NopTelemetry) EndResolutionKey(context.Context, string, TelemetryResolutionKeyOutcome) {}

func (NopTelemetry) WriteIdentificationAttempt(context.Context, TelemetryIdentificationAttempt) string {
	return ""
}

func (NopTelemetry) WriteIdentifierPluginCall(context.Context, TelemetryIdentifierPluginCall) {}

func (NopTelemetry) WriteDescriptionPluginCall(context.Context, TelemetryDescriptionPluginCall) {}

func (NopTelemetry) StartPriceGap(context.Context, TelemetryPriceGap) string { return "" }

func (NopTelemetry) EndPriceGap(context.Context, string, string, string) {}

func (NopTelemetry) WritePricePluginCall(context.Context, TelemetryPricePluginCall) {}

func (NopTelemetry) SweepIncompleteRuns(context.Context) (int64, error) { return 0, nil }

func (NopTelemetry) PurgeRunsBefore(context.Context, time.Time) (int64, error) { return 0, nil }
