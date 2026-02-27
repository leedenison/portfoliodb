package identifier

// Instrument holds canonical security-master data for an instrument.
// Identification plugins return enough data to find or create this in the DB.
type Instrument struct {
	ID         string  // UUID; may be empty when creating new
	AssetClass string  // e.g. equity, debt
	Exchange   string
	Currency   string
	Name       string  // optional display name
}

// Identifier is an opaque (type, value) pair for an instrument (e.g. CUSIP, ISIN, broker description).
// Broker descriptions use Type = broker name (e.g. "IBKR", "SCHB"), Value = full instrument_description.
type Identifier struct {
	Type  string // e.g. "CUSIP", "ISIN", "IBKR", "SCHB"
	Value string
}
