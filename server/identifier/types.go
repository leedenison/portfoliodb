package identifier

import "time"

// Asset class must be one of: EQUITY, ETF, MF, CASH, FIXED_INCOME, OPTION, FUTURE.
// When AssetClass is OPTION or FUTURE, Underlying and UnderlyingIdentifiers should be set so the service can set underlying_id.

// Instrument holds canonical security-master data for an instrument.
// Identification plugins return enough data to find or create this in the DB.
type Instrument struct {
	ID         string  // UUID; may be empty when creating new
	AssetClass string  // one of EQUITY, ETF, MF, CASH, FIXED_INCOME, OPTION, FUTURE
	Exchange   string
	Currency   string
	Name       string  // optional display name

	// Optional: when this instrument is a derivative (e.g. option, future), the plugin may provide underlying data.
	Underlying            *Instrument   // canonical data for the underlying instrument
	UnderlyingIdentifiers []Identifier  // identifiers for the underlying (used with Underlying to ensure underlying first)

	// Optional: when the instrument was available to trade on the exchange.
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// Identifier is an opaque (type, value) pair for an instrument (e.g. CUSIP, ISIN, broker description).
// Broker descriptions use Type = broker name (e.g. "IBKR", "SCHB"), Value = full instrument_description.
type Identifier struct {
	Type  string // e.g. "CUSIP", "ISIN", "IBKR", "SCHB"
	Value string
}
