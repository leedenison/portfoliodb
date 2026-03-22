package identifier

import "time"

// Asset class must be one of: STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN.
// When AssetClass is OPTION or FUTURE, UnderlyingIdentifiers should be set so the
// resolution layer can resolve the underlying through the full plugin pipeline.

// Instrument holds canonical security-master data for an instrument.
// Identification plugins return enough data to find or create this in the DB.
type Instrument struct {
	ID         string // UUID; may be empty when creating new
	AssetClass string // one of STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN
	Exchange   string
	Currency   string
	Name       string // optional display name

	// When this instrument is a derivative, plugins provide identifier hints for the
	// underlying. The resolution layer resolves the underlying through the full
	// plugin pipeline using these hints.
	UnderlyingIdentifiers []Identifier

	// Optional: when the instrument was available to trade on the exchange.
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// Identifier is an opaque (type, domain, value) for an instrument (e.g. CUSIP, ISIN, TICKER+exchange, broker description).
// Domain is optional (e.g. exchange code for TICKER). Broker descriptions use Type = source, Domain = "", Value = full instrument_description.
type Identifier struct {
	Type   string // e.g. "CUSIP", "ISIN", "TICKER", "FIGI"
	Domain string // optional; e.g. exchange code for TICKER
	Value  string
}
