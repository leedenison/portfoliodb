package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
)

// classificationRule maps OpenFIGI response fields to a PortfolioDB asset class.
// Non-nil set fields are ANDed: all must match. Nil means "don't care".
// Rules are evaluated in slice order; first match wins.
type classificationRule struct {
	assetClass     string
	securityTypes  map[string]bool // lowercased securityType values; nil = any
	securityType2s map[string]bool // lowercased securityType2 values; nil = any
	marketSectors  map[string]bool // lowercased marketSector values; nil = any
}

func toSet(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return m
}

// classificationRules is the ordered rule table for mapping OpenFIGI fields to
// asset class. Priority order: OPTION -> FUTURE -> ETF -> FX -> FIXED_INCOME ->
// MUTUAL_FUND -> STOCK -> CASH -> UNKNOWN.
var classificationRules = []classificationRule{
	// ── OPTION (100) ──
	{
		assetClass: db.AssetClassOption,
		securityTypes: toSet(
			"Equity Option", "Index Option", "Currency Option",
			"Physical index option", "Option on Equity Future",
			"OPTION", "OPTION VOLATILITY",
		),
	},
	{
		assetClass:     db.AssetClassOption,
		securityType2s: toSet("Option"),
	},

	// ── FUTURE (200) ──
	{
		assetClass: db.AssetClassFuture,
		securityTypes: toSet(
			"SINGLE STOCK FUTURE", "SINGLE STOCK DIVIDEND FUTURE",
			"SINGLE STOCK FUTURE SPREAD",
			"DIVIDEND NEUTRAL STOCK FUTURE",
			"Financial commodity future",
			"Physical commodity future",
			"Financial commodity forward",
			"Physical commodity forward",
			"NON-DELIVERABLE FORWARD", "ONSHORE FORWARD",
		),
	},
	{
		assetClass:     db.AssetClassFuture,
		securityType2s: toSet("Future"),
	},

	// ── ETF (300) ──
	{
		assetClass:    db.AssetClassETF,
		securityTypes: toSet("ETP"),
	},

	// ── FX (400) ──
	{
		assetClass: db.AssetClassFX,
		securityTypes: toSet(
			"Currency spot", "SPOT", "Currency WRT",
			"NDF SWAP", "ONSHORE SWAP",
		),
	},
	{
		assetClass:    db.AssetClassFX,
		marketSectors: toSet("Curncy"),
	},

	// ── FIXED_INCOME (500) ──
	{
		assetClass: db.AssetClassFixedIncome,
		securityTypes: toSet(
			"Bond", "MED TERM NOTE", "EURO MTN", "MEDIUM TERM CD",
			"COMMERCIAL PAPER", "EURO CP",
			"BANKERS ACCEPT", "BANKERS ACCEPTANCE",
			"DISCOUNT NOTES", "DEPOSIT NOTE", "BEARER DEP NOTE",
			"REPO", "FED FUNDS",
			"T-BILL", "PROV T-BILL",
			"MONETARY BILLS",
		),
	},
	{
		assetClass:     db.AssetClassFixedIncome,
		securityType2s: toSet("Corp", "Pool"),
	},
	{
		assetClass:    db.AssetClassFixedIncome,
		marketSectors: toSet("Corp", "Govt", "Muni", "Mtge", "M-Mkt"),
	},

	// ── MUTUAL_FUND (600) ──
	{
		assetClass: db.AssetClassMutualFund,
		securityTypes: toSet(
			"Open-End Fund", "Mutual Fund", "Closed-End Fund",
			"Unit Trust", "Savings Plan", "Savings Share",
			"Managed Account", "Pvt Eqty Fund", "MLP", "Ltd Part",
		),
	},
	{
		assetClass:     db.AssetClassMutualFund,
		securityType2s: toSet("Fund"),
	},

	// ── STOCK (700) ──
	{
		assetClass: db.AssetClassStock,
		securityTypes: toSet(
			"Common Stock", "Preference", "Preferred", "Pfd WRT",
			"ADR", "GDR", "BDR", "EDR", "NVDR", "SDR",
			"NY Reg Shrs", "Dutch Cert", "Austrian Crt",
			"Belgian Cert", "Participate Cert",
			"Depositary Receipt", "Receipt",
			"Stapled Security", "Right", "REIT",
			"Contract For Difference",
		),
	},
	{
		assetClass:     db.AssetClassStock,
		securityType2s: toSet("Common Stock"),
	},
	{
		assetClass:    db.AssetClassStock,
		marketSectors: toSet("Equity"),
	},

	// ── CASH (900) ──
	{
		assetClass:    db.AssetClassCash,
		securityTypes: toSet("CASH"),
	},

	// ── UNKNOWN (999) -- terminal fallback ──
	{assetClass: db.AssetClassUnknown},
}

// classify maps OpenFIGI securityType/securityType2/marketSector to a
// PortfolioDB asset class using the ordered rule table. Always returns a
// non-empty value (UNKNOWN at minimum).
func classify(securityType, securityType2, marketSector string) string {
	st := strings.ToLower(strings.TrimSpace(securityType))
	st2 := strings.ToLower(strings.TrimSpace(securityType2))
	ms := strings.ToLower(strings.TrimSpace(marketSector))
	for _, r := range classificationRules {
		if r.securityTypes != nil && !r.securityTypes[st] {
			continue
		}
		if r.securityType2s != nil && !r.securityType2s[st2] {
			continue
		}
		if r.marketSectors != nil && !r.marketSectors[ms] {
			continue
		}
		return r.assetClass
	}
	return db.AssetClassUnknown
}

// openFIGIResultToInstrument converts one OpenFIGI result to identifier.Instrument and identifiers.
// If the result is a derivative (option/future), underlying is resolved separately and set on inst.
// exchMap may be nil; when present, ExchCode is resolved to an ISO MIC for the Exchange field.
func openFIGIResultToInstrument(r *OpenFIGIResult, exchMap *exchangemap.ExchangeMap) (*identifier.Instrument, []identifier.Identifier) {
	assetClass := classify(r.SecurityType, r.SecurityType2, r.MarketSector)
	name := r.Name
	if name == "" {
		name = r.SecurityDescription
	}
	if name == "" {
		name = r.Ticker
	}
	inst := &identifier.Instrument{
		AssetClass: assetClass,
		Exchange:   resolveExchange(r.ExchCode, exchMap),
		Currency:   "", // OpenFIGI often omits; leave empty
		Name:       name,
	}
	if r.FIGI != "" {
		inst.ProviderIdentifiers = append(inst.ProviderIdentifiers,
			identifier.ProviderIdentifier{Provider: "openfigi", Type: "FIGI", Value: r.FIGI})
	}
	var ids []identifier.Identifier
	if r.ShareClassFIGI != nil && *r.ShareClassFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_SHARE_CLASS", Value: *r.ShareClassFIGI})
	}
	if r.CompositeFIGI != nil && *r.CompositeFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_COMPOSITE", Value: *r.CompositeFIGI})
	}
	if r.Ticker != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_TICKER", Domain: r.ExchCode, Value: identifier.NormalizeSplitTicker(r.Ticker, ".")})
	}
	return inst, ids
}

// resolveExchange maps an OpenFIGI exchange code to the first operating MIC.
func resolveExchange(exchCode string, exchMap *exchangemap.ExchangeMap) string {
	if exchMap == nil || exchCode == "" {
		return ""
	}
	mics := exchMap.ExchCodeToMICs(exchCode)
	if len(mics) == 0 {
		return ""
	}
	return mics[0]
}

// isDerivative returns true if the result is an option or future.
func isDerivative(r *OpenFIGIResult) bool {
	ac := classify(r.SecurityType, r.SecurityType2, r.MarketSector)
	return ac == db.AssetClassOption || ac == db.AssetClassFuture
}
