package identifier

import "github.com/leedenison/portfoliodb/server/db"

// Security type hint vocabulary. Same as asset class (type alias). Plugins use these as keys in AcceptableSecurityTypes() and compare against Hints.SecurityTypeHint.
const (
	SecurityTypeHintStock       = db.AssetClassStock
	SecurityTypeHintETF         = db.AssetClassETF
	SecurityTypeHintFixedIncome = db.AssetClassFixedIncome
	SecurityTypeHintMutualFund  = db.AssetClassMutualFund
	SecurityTypeHintOption      = db.AssetClassOption
	SecurityTypeHintFuture      = db.AssetClassFuture
	SecurityTypeHintCash        = db.AssetClassCash
	SecurityTypeHintFX          = db.AssetClassFX
	SecurityTypeHintUnknown     = db.AssetClassUnknown
)

// Hints are optional resolution hints passed to description and identifier plugins.
// SecurityTypeHint is derived from the transaction type and used for plugin routing; vocabulary is the same as asset class.
type Hints struct {
	ExchangeCode   string
	Currency       string
	MIC            string
	SecurityTypeHint string
}

// UnderlyingSecTypeHint returns the inferred security type for a derivative's
// underlying. Returns "" if the asset class is not a derivative.
func UnderlyingSecTypeHint(derivativeAssetClass string) string {
	switch derivativeAssetClass {
	case db.AssetClassOption, db.AssetClassFuture:
		return SecurityTypeHintStock
	default:
		return ""
	}
}

// AllowedIdentifierTypes is the controlled vocabulary for identifier hint types (proto IdentifierType names).
// Description plugins must return hints whose Type is in this set; invalid types are discarded at debug log.
var AllowedIdentifierTypes = map[string]bool{
	"ISIN": true, "CUSIP": true, "SEDOL": true, "CINS": true, "WERTPAPIER": true,
	"OCC": true, "OPRA": true, "FUT_OPT": true,
	"OPENFIGI_GLOBAL": true, "OPENFIGI_SHARE_CLASS": true, "OPENFIGI_COMPOSITE": true,
	"TICKER": true, "BROKER_DESCRIPTION": true,
	"CURRENCY": true,
	"FX_PAIR":  true,
}
