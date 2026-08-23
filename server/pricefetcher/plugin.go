// Package pricefetcher defines the price plugin interface and orchestrator.
//
// Price plugins fetch end-of-day (EOD) OHLCV bars from external data providers
// (e.g. Massive, EODHD). The orchestrator (worker.go) coordinates plugin
// invocation to fill price gaps -- date ranges where users held instruments but
// no cached prices exist.
//
// Plugin matching and filtering:
//
// The unit of work is a listing -- one currency line of a security -- because a
// price is quoted in a currency. The orchestrator skips a plugin for a listing
// when the security's asset class, or the listing's currency or venue set, is
// non-empty and not in the plugin's acceptable set. Empty values always pass the
// filter, so an unclassified security or a line no provider has named a venue for
// can be attempted by any plugin.
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

	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// ErrNoData indicates the plugin cannot provide price data for this listing.
// The orchestrator tries the next plugin in precedence order.
var ErrNoData = errors.New("no price data available")

// ErrPermanent indicates a permanent failure for a (listing, plugin) pair
// (e.g. HTTP 403, 404). The orchestrator creates a fetch block so this
// combination is never retried. The block names the line, so a provider that
// refuses one currency line of a security keeps being asked about its others.
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
	// only identifiers of these types to FetchPrices, and only those naming the
	// listing being fetched or the security above it -- never a sibling line's,
	// which would be a name for a line the plugin was not asked about. A listing
	// is eligible for this plugin if it has ANY of these identifier types.
	SupportedIdentifierTypes() []string

	// AcceptableAssetClasses returns asset classes this plugin handles.
	// nil or empty = all. A security with a non-null asset class not in this
	// set is skipped. The asset class is the security's: a security is one kind
	// of thing whichever currency it trades in.
	AcceptableAssetClasses() map[string]bool

	// AcceptableExchanges returns the operating MICs this plugin handles.
	// nil or empty = all, and so is a listing admitted to no venue -- nothing
	// named one, so there is nothing to fail on. A listing admitted to several
	// passes when the plugin carries any of them.
	AcceptableExchanges() map[string]bool

	// AcceptableCurrencies returns currencies this plugin handles.
	// nil or empty = all. The currency tested is the listing's own, which is
	// what the bars will be denominated in. A listing with no currency never
	// reaches here, being unpriceable.
	AcceptableCurrencies() map[string]bool

	// FetchPrices fetches EOD bars for the given listing over [from, before).
	// identifiers contains only the types declared by SupportedIdentifierTypes.
	// assetClass is the security's asset class so the plugin can adjust
	// behavior (e.g. stock ticker vs option OCC symbol format).
	// Returns ErrNoData when the plugin has no data for this listing.
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
//
// Derived from currency.MinorUnits rather than written out, because "GBX is GBP
// under a different prefix" is also what the listing uniqueness index and the
// OpenFIGI currency filter key on. Every pair pivots on USD (adr/0006), which is
// the quote currency every FX instrument is seeded against.
var DerivedFXPairs = derivedFXPairs()

func derivedFXPairs() map[string]DerivedFXPair {
	m := make(map[string]DerivedFXPair, len(currency.MinorUnits))
	for _, u := range currency.MinorUnits {
		m[u.Code+"USD"] = DerivedFXPair{SourcePair: u.Major + "USD", Exponent: u.Exponent}
	}
	return m
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

// ScaleBars shifts every price field -- Open, High, Low, Close and
// AdjustedClose -- by exp powers of ten. Volume is a share count rather than a
// price and is left unchanged. Returns a new slice.
//
// The rebuild is field by field rather than a copy of b, so a price field added
// to DailyBar and not named here is silently dropped instead of silently left
// unscaled. Dropping is the safer of the two, a missing value being visible in a
// way a wrongly-scaled one is not, but neither is correct: add the field.
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
		if b.AdjustedClose != nil {
			v := b.AdjustedClose.Shift(exp)
			out[i].AdjustedClose = &v
		}
	}
	return out
}
