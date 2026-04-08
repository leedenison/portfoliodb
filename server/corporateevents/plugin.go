// Package corporateevents defines the corporate event plugin interface and
// orchestrator. Corporate event plugins fetch stock splits and cash dividends
// from external data providers (e.g. Massive, EODHD).
//
// The orchestrator (worker.go) iterates held STOCK/ETF instruments, computes
// the missing date interval per (instrument, plugin) from the
// corporate_event_coverage table, and invokes plugins in precedence order.
// Each successful fetch -- including a fetch that returns an empty result --
// records a coverage row tagged with the plugin id, so a plugin's authoritative
// "no events here" answer is not redundantly re-asked on the next cycle.
//
// Plugin matching mirrors pricefetcher: the orchestrator skips a plugin for an
// instrument when its asset class, exchange, or currency is non-null and not in
// the plugin's acceptable set. Null values on the instrument always pass.
package corporateevents

import (
	"context"
	"time"
)

// Plugin is the corporate event fetcher plugin interface. Implementations
// live under server/plugins/<datasource>/corporateevents.
type Plugin interface {
	// DisplayName returns a human-readable name shown in the admin UI.
	DisplayName() string

	// SupportedIdentifierTypes returns identifier types this plugin can use to
	// look up corporate events (e.g. ["MIC_TICKER", "OPENFIGI_TICKER"]). The
	// orchestrator passes only identifiers of these types to FetchEvents. An
	// instrument is eligible for this plugin if it has any of these types.
	SupportedIdentifierTypes() []string

	// AcceptableAssetClasses returns asset classes this plugin handles.
	// nil or empty = all. In practice plugins should return {STOCK, ETF}.
	AcceptableAssetClasses() map[string]bool

	// AcceptableExchanges returns exchange MICs this plugin handles.
	// nil or empty = all. Instruments with a null exchange always pass.
	AcceptableExchanges() map[string]bool

	// AcceptableCurrencies returns currencies this plugin handles.
	// nil or empty = all. Instruments with a null currency always pass.
	AcceptableCurrencies() map[string]bool

	// FetchEvents fetches stock splits and cash dividends for the given
	// instrument over the closed interval [from, to]. identifiers contains
	// only the types declared by SupportedIdentifierTypes. assetClass is the
	// instrument's DB asset class string.
	//
	// An empty result (zero splits, zero dividends) is a valid authoritative
	// answer: the orchestrator records coverage and stops trying lower-
	// precedence plugins. Use ErrPermanent or ErrTransient to signal failures
	// instead of returning an empty result for a real error.
	FetchEvents(ctx context.Context, config []byte, identifiers []Identifier, assetClass string, from, to time.Time) (*Events, error)

	// DefaultConfig returns the plugin's default config JSON. Inserted on
	// startup when no row exists so the admin can edit via the UI.
	DefaultConfig() []byte
}

// Identifier is a minimal (type, domain, value) tuple passed to plugins.
// Defined here to avoid importing server/identifier.
type Identifier struct {
	Type   string
	Domain string
	Value  string
}

// Events groups the splits and cash dividends returned by a single
// FetchEvents call. Either or both slices may be empty.
type Events struct {
	Splits        []Split
	CashDividends []CashDividend
}

// Split is a single stock split returned by a plugin. SplitFrom and SplitTo
// are the raw halves of the split ratio expressed as decimal strings so
// providers' rationals (e.g. "7/1") are preserved without precision loss.
type Split struct {
	ExDate    time.Time
	SplitFrom string
	SplitTo   string
}

// CashDividend is a single cash dividend returned by a plugin. Optional
// fields are zero (time.Time{}, "") when the provider does not supply them.
type CashDividend struct {
	ExDate          time.Time
	PayDate         time.Time
	RecordDate      time.Time
	DeclarationDate time.Time
	Amount          string // decimal string per share
	Currency        string
	Frequency       string
}
