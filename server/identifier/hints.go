package identifier

import "github.com/leedenison/portfoliodb/server/db"

// Instrument kind vocabulary. Coarser than asset class; used as first-pass
// plugin filter so that cash plugins never see securities and vice versa.
const (
	InstrumentKindCash     = db.InstrumentKindCash
	InstrumentKindSecurity = db.InstrumentKindSecurity
)

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
// InstrumentKind is a coarse filter (CASH vs SECURITY); SecurityTypeHint is the
// fine-grained asset class derived from the transaction type.
type Hints struct {
	Currency         string
	InstrumentKind   string
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
func UnderlyingSecTypeHint(derivativeAssetClass string) string {
	if !db.IsDerivative(derivativeAssetClass) {
		return ""
	}
	return SecurityTypeHintStock
}

// ShouldAttemptPlugin returns whether a plugin should be tried given the
// hint's instrument kind and security type. The kind gate is checked first:
// if both the plugin and hint declare a kind, they must match. The type gate
// is checked second but skipped when the hint type is UNKNOWN (meaning "we
// know the kind but not the specific asset class").
func ShouldAttemptPlugin(acceptableKinds, acceptableTypes map[string]bool, kind, secType string) bool {
	if len(acceptableKinds) > 0 && kind != "" && !acceptableKinds[kind] {
		return false
	}
	if len(acceptableTypes) > 0 && secType != "" && secType != SecurityTypeHintUnknown && !acceptableTypes[secType] {
		return false
	}
	return true
}

// HintDiff records a single difference between a supplied hint and the
// resolved instrument value.
type HintDiff struct {
	Field         string // "Currency", "SecurityType", "Exchange", or identifier type e.g. "ISIN"
	HintValue     string
	ResolvedValue string
}
