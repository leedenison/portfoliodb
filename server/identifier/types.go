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

	CIK     string // SEC Central Index Key (optional)
	SICCode string // SIC industry classification code (optional)

	// When this instrument is a derivative, plugins provide identifier hints for the
	// underlying. The resolution layer resolves the underlying through the full
	// plugin pipeline using these hints.
	UnderlyingIdentifiers []Identifier

	// Optional: when the instrument was available to trade on the exchange.
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// Identifier is an opaque (type, domain, value) for an instrument (e.g. CUSIP, ISIN, MIC_TICKER+MIC, broker description).
// Domain is optional. For MIC_TICKER, domain is an ISO 10383 MIC code (empty when unknown).
// For OPENFIGI_TICKER, domain is a Bloomberg/OpenFIGI exchange code (e.g. "US").
// Broker descriptions use Type = source, Domain = "", Value = full instrument_description.
type Identifier struct {
	Type   string // e.g. "CUSIP", "ISIN", "MIC_TICKER", "OPENFIGI_TICKER"
	Domain string // optional; MIC for MIC_TICKER, exchange code for OPENFIGI_TICKER
	Value  string
}
