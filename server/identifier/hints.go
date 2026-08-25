package identifier

import "github.com/leedenison/portfoliodb/server/db"

// Security type hint vocabulary: aliases of the asset class constants, because
// the hint is a value of that vocabulary and not a second one. Plugins use
// these as keys in AcceptableSecurityTypes() and compare against
// Hints.SecurityTypeHint. The hierarchy over them is in
// [github.com/leedenison/portfoliodb/server/assetclass].
const (
	SecurityTypeHintUnknown     = db.AssetClassUnknown
	SecurityTypeHintCash        = db.AssetClassCash
	SecurityTypeHintSecurity    = db.AssetClassSecurity
	SecurityTypeHintEquity      = db.AssetClassEquity
	SecurityTypeHintStock       = db.AssetClassStock
	SecurityTypeHintETF         = db.AssetClassETF
	SecurityTypeHintMutualFund  = db.AssetClassMutualFund
	SecurityTypeHintFixedIncome = db.AssetClassFixedIncome
	SecurityTypeHintDerivative  = db.AssetClassDerivative
	SecurityTypeHintOption      = db.AssetClassOption
	SecurityTypeHintFuture      = db.AssetClassFuture
	SecurityTypeHintFX          = db.AssetClassFX
)

// Hints are optional resolution hints passed to description and identifier
// plugins. SecurityTypeHint is the asset class the source stated, at whatever
// specificity it could defend: a broker statement that cannot tell a share from
// an ETF says EQUITY, and a row whose source said only that it is not money
// says SECURITY.
type Hints struct {
	Currency         string
	SecurityTypeHint string
}

// USComposite is the composite exchange code for US listings. OpenFIGI spells
// it this way as exchCode and EODHD as its exchange filter, so one hint domain
// constrains both. An OCC symbol names a US-listed underlying by construction,
// so a hint derived from one carries this rather than leaving the venue open: a
// bare root matches that ticker on every venue in the world, and which of them
// is chosen is then arbitrary.
const USComposite = "US"

// UnderlyingSecTypeHint returns the inferred security type for a derivative's
// underlying. Returns "" if the asset class is not a derivative.
//
// EQUITY and not STOCK: a listed option is written on a share or on a fund --
// the options on SPY are not options on a company -- and the contract symbol
// says nothing about which. Claiming the leaf would state something the
// contract does not carry, and the plugins a share reaches are the ones a fund
// reaches.
func UnderlyingSecTypeHint(derivativeAssetClass string) string {
	if !db.IsDerivative(derivativeAssetClass) {
		return ""
	}
	return SecurityTypeHintEquity
}

// HintDiff records a single difference between a supplied hint and the
// resolved instrument value.
type HintDiff struct {
	Field         string // "Currency", "SecurityType", "Exchange", or identifier type e.g. "ISIN"
	HintValue     string
	ResolvedValue string
}
