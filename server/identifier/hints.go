package identifier

import (
	"github.com/leedenison/portfoliodb/server/assetclass"
	"github.com/leedenison/portfoliodb/server/db"
)

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

// ShouldAttemptPlugin reports whether a plugin should be tried for a row whose
// source stated secType. A plugin declaring no acceptable types takes anything,
// and a row whose source stated nothing is offered to every plugin.
//
// The permissive question: a plugin is tried when what the source said and what
// the plugin covers could describe one security. Excluding a row because its
// source could not be specific loses the row, where trying a plugin that turns
// out not to cover it costs a call -- which is why a statement of EQUITY
// reaches a plugin declaring STOCK, and why a source that said only SECURITY
// reaches all of them.
//
// Cash and securities stay apart under the same rule rather than a second gate
// above it: a cash plugin declares CASH, which no security class lies under or
// over, so only a row whose source stated cash can reach one.
func ShouldAttemptPlugin(acceptable map[string]bool, secType string) bool {
	if len(acceptable) == 0 || secType == "" {
		return true
	}
	stated := db.StrToAssetClass(secType)
	for t := range acceptable {
		if assetclass.MayBe(stated, db.StrToAssetClass(t)) {
			return true
		}
	}
	return false
}

// HintDiff records a single difference between a supplied hint and the
// resolved instrument value.
type HintDiff struct {
	Field         string // "Currency", "SecurityType", "Exchange", or identifier type e.g. "ISIN"
	HintValue     string
	ResolvedValue string
}
