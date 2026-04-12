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

// AllowedIdentifierTypes is the controlled vocabulary for identifier hint types (proto IdentifierType names).
// Description plugins must return hints whose Type is in this set; invalid types are discarded at debug log.
var AllowedIdentifierTypes = map[string]bool{
	"ISIN": true, "CUSIP": true, "SEDOL": true, "CINS": true, "WERTPAPIER": true,
	"OCC": true, "OPRA": true, "FUT_OPT": true,
	"OPENFIGI_GLOBAL": true, "OPENFIGI_SHARE_CLASS": true, "OPENFIGI_COMPOSITE": true,
	"MIC_TICKER": true, "OPENFIGI_TICKER": true, "BROKER_DESCRIPTION": true,
	"CURRENCY": true,
	"FX_PAIR":  true,
}
