package identifier

// Hints are optional resolution hints passed to description and identifier plugins.
// SecurityType is derived from the transaction type (e.g. Bond, Option, Equity) and may be sent to OpenFIGI as securityType2.
type Hints struct {
	ExchangeCode string
	Currency     string
	MIC          string
	SecurityType string
}

// AllowedIdentifierTypes is the controlled vocabulary for identifier hint types (proto IdentifierType names).
// Description plugins must return hints whose Type is in this set; invalid types are discarded at debug log.
var AllowedIdentifierTypes = map[string]bool{
	"ISIN": true, "CUSIP": true, "SEDOL": true, "CINS": true, "WERTPAPIER": true,
	"OCC": true, "OPRA": true, "FUT_OPT": true,
	"OPENFIGI_GLOBAL": true, "OPENFIGI_SHARE_CLASS": true, "OPENFIGI_COMPOSITE": true,
	"TICKER": true, "BROKER_DESCRIPTION": true,
	"CURRENCY": true,
}
