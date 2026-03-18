// Package pricefetcher defines the price plugin interface and orchestrator.
//
// Price plugins fetch end-of-day (EOD) OHLCV bars from external data providers
// (e.g. Massive, EODHD). The orchestrator (worker.go) coordinates plugin
// invocation to fill price gaps -- date ranges where users held instruments but
// no cached prices exist.
//
// Plugin matching and filtering:
//
// The orchestrator skips a plugin for an instrument when the instrument's
// asset class, exchange, or currency is non-null and not in the plugin's
// acceptable set. Null values on the instrument always pass the filter --
// this allows unclassified instruments to be attempted by any plugin.
//
// Rate limit strategy:
//
// Each plugin manages its own rate limiter. Plugins sharing an API key (e.g.
// Massive identifier + Massive price) maintain separate limiters set to the
// configured calls-per-minute. When both run simultaneously the combined rate
// may exceed the provider's quota; 429 responses are handled with backoff and
// the instrument is retried on the next cycle.
package pricefetcher

import (
	"context"
	"errors"
	"time"
)

// ErrNoData indicates the plugin cannot provide price data for this
// instrument. The orchestrator tries the next plugin in precedence order.
var ErrNoData = errors.New("no price data available")

// DailyBar is one day of OHLCV data. Close is always required; other fields
// are optional (nil = not available from the provider).
type DailyBar struct {
	Date   time.Time
	Open   *float64
	High   *float64
	Low    *float64
	Close  float64
	Volume *int64
}

// FetchResult holds the bars returned by a plugin for a single request.
type FetchResult struct {
	Bars []DailyBar
}

// Identifier is a minimal (type, domain, value) tuple passed to plugins.
// Defined here to avoid importing server/identifier.
type Identifier struct {
	Type   string
	Domain string
	Value  string
}

// Plugin is the price fetcher plugin interface. Implementations live under
// server/plugins/<datasource>/price (e.g. server/plugins/massive/price).
type Plugin interface {
	// DisplayName returns a human-readable name shown in the admin UI.
	DisplayName() string

	// SupportedIdentifierTypes returns identifier types this plugin can use
	// to look up prices (e.g. ["TICKER", "OCC"]). The orchestrator passes
	// only identifiers of these types to FetchPrices. An instrument is
	// eligible for this plugin if it has ANY of these identifier types.
	SupportedIdentifierTypes() []string

	// AcceptableAssetClasses returns asset classes this plugin handles.
	// nil or empty = all. Instruments with a non-null asset class not in
	// this set are skipped.
	AcceptableAssetClasses() map[string]bool

	// AcceptableExchanges returns exchange codes this plugin handles.
	// nil or empty = all. Instruments with a null exchange always pass.
	AcceptableExchanges() map[string]bool

	// AcceptableCurrencies returns currencies this plugin handles.
	// nil or empty = all. Instruments with a null currency always pass.
	AcceptableCurrencies() map[string]bool

	// FetchPrices fetches EOD bars for the given instrument over [from, to).
	// identifiers contains only the types declared by SupportedIdentifierTypes.
	// assetClass is the instrument's asset class so the plugin can adjust
	// behavior (e.g. stock ticker vs option OCC symbol format).
	// Returns ErrNoData when the plugin has no data for this instrument.
	FetchPrices(ctx context.Context, config []byte, identifiers []Identifier, assetClass string, from, to time.Time) (*FetchResult, error)

	// DefaultConfig returns the plugin's default config JSON. Inserted on
	// startup when no row exists so the admin can edit via the UI.
	DefaultConfig() []byte
}
