// Package inflationfetcher defines the inflation plugin interface and orchestrator.
//
// Inflation plugins fetch monthly consumer price index values from external
// data providers (e.g. ONS). The orchestrator (worker.go) coordinates plugin
// invocation to fill gaps in inflation coverage for currencies that users have
// selected as their display currency.
package inflationfetcher

import (
	"context"
	"errors"
	"time"
)

// ErrNoData indicates the plugin cannot provide inflation data for this
// currency. The orchestrator tries the next plugin in precedence order.
var ErrNoData = errors.New("no inflation data available")

// MonthlyIndex is one month of inflation index data.
type MonthlyIndex struct {
	Month      time.Time // 1st of month, UTC
	IndexValue float64
	BaseYear   int // year where July = 100
}

// FetchResult holds the indices returned by a plugin for a single request.
type FetchResult struct {
	Indices []MonthlyIndex
}

// Plugin is the inflation fetcher plugin interface. Implementations live under
// server/plugins/<datasource>/inflation (e.g. server/plugins/ons/inflation).
type Plugin interface {
	// DisplayName returns a human-readable name shown in the admin UI.
	DisplayName() string

	// SupportedCurrencies returns ISO 4217 currency codes this plugin can
	// provide inflation data for (e.g. ["GBP"]). The orchestrator skips
	// currencies not in this list.
	SupportedCurrencies() []string

	// FetchInflation fetches monthly inflation indices for the given currency
	// over [from, to). Returns ErrNoData when the plugin has no data for
	// this currency.
	FetchInflation(ctx context.Context, config []byte, currency string, from, to time.Time) (*FetchResult, error)

	// DefaultConfig returns the plugin's default config JSON. Inserted on
	// startup when no row exists so the admin can edit via the UI.
	DefaultConfig() []byte
}
