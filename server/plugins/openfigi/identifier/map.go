package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/currency"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/openfigi/exchangemap"
)

// openFIGICurrencyOverrides maps ISO minor-unit currency codes to the
// Bloomberg-style codes that the OpenFIGI Mapping API expects.
// For example, GBX (ISO pence sterling) must be sent as "GBp".
//
// Which currencies are minor units of which is currency.MinorUnits; how
// Bloomberg spells one is this package's business, and the convention is the
// major code with its final letter lowercased -- GBP becomes GBp.
var openFIGICurrencyOverrides = currencyOverrides()

func currencyOverrides() map[string]string {
	m := make(map[string]string, len(currency.MinorUnits))
	for _, u := range currency.MinorUnits {
		m[u.Code] = u.Major[:len(u.Major)-1] + strings.ToLower(u.Major[len(u.Major)-1:])
	}
	return m
}

// toOpenFIGICurrency converts a currency code to the form expected by the
// OpenFIGI API. Most codes pass through unchanged; known minor-unit codes
// (e.g. GBX) are replaced with their Bloomberg equivalents.
func toOpenFIGICurrency(code string) string {
	if v, ok := openFIGICurrencyOverrides[code]; ok {
		return v
	}
	return code
}

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
// MUTUAL_FUND -> STOCK -> SECURITY -> CASH -> UNKNOWN.
//
// The rules run from what the provider named to what it merely implied, and the
// answer says which it was: a securityType of "Common Stock" is a share, while
// the market sector on its own is the last thing left to read. OpenFIGI's own
// guidance is to fall back to it when securityType2 is absent, which is what
// the rule at the bottom does.
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
	// ── SECURITY (800) ──
	//
	// The market sector alone, with no securityType or securityType2 this table
	// knows. SECURITY and not EQUITY: the Equity sector is not a statement that
	// the security is a shareholding. Bloomberg files equity options, single
	// stock futures, warrants and rights under it as readily as shares and
	// funds -- an equity option's ticker ends in "Equity", and the recorded
	// responses in testdata show securityType "Equity Option" carrying
	// marketSector "Equity", two of them with a securityType2 no rule above
	// matches. What the sector rules out is debt, currency and commodity, and
	// the vocabulary has no node for exactly that, so the answer is the nearest
	// one that is true.
	{
		assetClass:    db.AssetClassSecurity,
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
		Listing: identifier.Listing{
			Currency: "", // OpenFIGI does not return currency; caller sets from hints
			Venue:    resolveVenue(r.ExchCode, exchMap),
		},
		Name: name,
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

// resolveVenue reads an OpenFIGI exchange code as what it says about where the
// instrument trades: an operating MIC when the code names a venue, the country
// when it names a composite, and nothing when the code is unknown.
//
// A composite deliberately yields no MIC. It spans a market, and picking a
// member of it would store a venue nobody stated -- which then reads as a
// contradiction of the correct venue when another plugin supplies one. The code
// itself is not lost: it travels as the domain of the OPENFIGI_TICKER
// identifier.
//
// The handful of venue codes covering more than one operating MIC are treated
// the same way, since they are no more specific than a composite.
func resolveVenue(exchCode string, exchMap *exchangemap.ExchangeMap) identifier.Venue {
	if exchMap == nil || exchCode == "" {
		return identifier.Venue{}
	}
	if mics := exchMap.ExchCodeToMICs(exchCode); len(mics) == 1 {
		return identifier.Venue{MIC: mics[0]}
	}
	return identifier.Venue{Country: exchMap.CompositeCountry(exchCode)}
}

// isDerivative reports whether the result classifies as a derivative. The
// provider's own vocabulary is mapped to an asset class first, so which classes
// those are is asked of db.IsDerivative rather than restated here.
func isDerivative(r *OpenFIGIResult) bool {
	return db.IsDerivative(classify(r.SecurityType, r.SecurityType2, r.MarketSector))
}
