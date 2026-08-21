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

	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// ErrNoData indicates the plugin cannot provide price data for this
// instrument. The orchestrator tries the next plugin in precedence order.
var ErrNoData = errors.New("no price data available")

// ErrPermanent indicates a permanent failure for an (instrument, plugin)
// pair (e.g. HTTP 403, 404). The orchestrator creates a fetch block so
// this combination is never retried.
type ErrPermanent struct{ Reason string }

func (e *ErrPermanent) Error() string { return "permanent: " + e.Reason }

// DailyBar is one day of OHLCV data. Close is always required; other fields
// are optional (nil = not available from the provider).
type DailyBar struct {
	Date   time.Time
	Open   *decimal.Decimal
	High   *decimal.Decimal
	Low    *decimal.Decimal
	Close  decimal.Decimal
	Volume *int64
	// AdjustedClose is the provider's own adjusted close when it supplies one.
	// Stored for cross-checking; never an input to valuation.
	AdjustedClose *decimal.Decimal
}

// ShareCountBasis states which share count a plugin's bars are denominated in.
// It cannot be inferred from the fetch: an as-traded series is expressed in the
// share count current on each bar's own date, while a back-adjusted series is
// expressed in the share count current when the provider answered. Plugins
// declare it so the storage layer never has to assume.
// See docs/spec/bitemporality.md.
type ShareCountBasis int

const (
	// AsTraded means each bar is denominated in the share count current on its
	// own date. This is what a provider returns when asked for unadjusted data.
	AsTraded ShareCountBasis = iota
	// AsOfFetch means the provider back-adjusted the whole series to the share
	// count current when it answered.
	AsOfFetch
)

// FetchResult holds the bars returned by a plugin for a single request.
type FetchResult struct {
	Bars []DailyBar
	// ShareCountBasis defaults to AsTraded, which is correct for every plugin
	// that requests unadjusted data.
	ShareCountBasis ShareCountBasis
}

// Plugin is the price fetcher plugin interface. Implementations live under
// server/plugins/<datasource>/price (e.g. server/plugins/massive/price).
type Plugin interface {
	// DisplayName returns a human-readable name shown in the admin UI.
	DisplayName() string

	// SupportedIdentifierTypes returns identifier types this plugin can use
	// to look up prices (e.g. ["MIC_TICKER", "OPENFIGI_TICKER", "OCC"]). The orchestrator passes
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

	// FetchPrices fetches EOD bars for the given instrument over [from, before).
	// identifiers contains only the types declared by SupportedIdentifierTypes.
	// assetClass is the instrument's asset class so the plugin can adjust
	// behavior (e.g. stock ticker vs option OCC symbol format).
	// Returns ErrNoData when the plugin has no data for this instrument.
	//
	// Derived FX pairs: some FX pairs cannot be fetched directly from data
	// providers (e.g. GBXUSD for British pence). Plugins must handle these
	// by fetching the source pair and scaling the result. Use RewriteFXPair
	// to detect derived pairs and ScaleBars to apply the conversion.
	FetchPrices(ctx context.Context, config []byte, identifiers []identifier.Identifier, assetClass string, from, before time.Time) (*FetchResult, error)

	// DefaultConfig returns the plugin's default config JSON. Inserted on
	// startup when no row exists so the admin can edit via the UI.
	DefaultConfig() []byte
}

// DerivedFXPair describes an FX pair that is derived from another by a change of
// currency unit prefix, which moves the decimal point rather than dividing.
type DerivedFXPair struct {
	SourcePair string
	// Exponent is the power of ten to shift the source pair's rates by.
	Exponent int32
}

// DerivedFXPairs maps FX pair values that cannot be fetched directly to their
// source pair and exponent. GBXUSD (British pence) is derived from GBPUSD by
// shifting two places, because one pence is 10^-2 pounds -- GBX and GBP are the
// same unit under a different prefix. Recording the exponent rather than a
// divisor keeps the rates exact: a decimal-point shift is multiplication by a
// power of ten, which is closed, whereas dividing would round at whatever
// precision the division picked. See adr/0026-exact-decimals-bounded-by-closure.md.
var DerivedFXPairs = map[string]DerivedFXPair{
	"GBXUSD": {SourcePair: "GBPUSD", Exponent: -2},
}

// RewriteFXPair checks whether value is a derived FX pair. If so it returns the
// source pair and the exponent to shift its rates by; otherwise it returns value
// unchanged with exponent 0.
func RewriteFXPair(value string) (string, int32) {
	if d, ok := DerivedFXPairs[value]; ok {
		return d.SourcePair, d.Exponent
	}
	return value, 0
}

// ScaleBars shifts all price fields (Open, High, Low, Close) by exp powers of
// ten. Volume is left unchanged. Returns a new slice.
func ScaleBars(bars []DailyBar, exp int32) []DailyBar {
	out := make([]DailyBar, len(bars))
	for i, b := range bars {
		out[i] = DailyBar{
			Date:   b.Date,
			Close:  b.Close.Shift(exp),
			Volume: b.Volume,
		}
		if b.Open != nil {
			v := b.Open.Shift(exp)
			out[i].Open = &v
		}
		if b.High != nil {
			v := b.High.Shift(exp)
			out[i].High = &v
		}
		if b.Low != nil {
			v := b.Low.Shift(exp)
			out[i].Low = &v
		}
	}
	return out
}
