package identifier

import (
	"log/slog"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// openFIGIFutureSecurityTypes is the allowlist for FUTURE asset class (exact match, case-insensitive).
// Values from OpenFIGI securityType/securityType2.
var openFIGIFutureSecurityTypes = map[string]bool{
	"future": true, "currency future": true, "financial commodity future": true,
	"financial index future": true, "generic currency future": true, "generic index future": true,
	"financial commodity generic": true, "dividend neutral stock future": true,
}

// assetClassFromOpenFIGI maps OpenFIGI securityType/securityType2/marketSector to PortfolioDB asset class.
// Order: Option → Future (allowlist) → ETP+Mutual Fund→ETF, Mutual Fund→MUTUAL_FUND, ETP→ETF → STOCK → FIXED_INCOME → CASH → fall-through.
// When no rule matches, returns "" and if log != nil logs that asset class could not be determined.
func assetClassFromOpenFIGI(securityType, securityType2, marketSector string, log *slog.Logger) string {
	s := strings.TrimSpace(strings.ToLower(securityType))
	s2 := strings.TrimSpace(strings.ToLower(securityType2))
	m := strings.TrimSpace(strings.ToLower(marketSector))

	if strings.Contains(s2, "option") || strings.Contains(s, "option") {
		return db.AssetClassOption
	}
	if openFIGIFutureSecurityTypes[s] || openFIGIFutureSecurityTypes[s2] {
		return db.AssetClassFuture
	}
	if s == "etp" && s2 == "mutual fund" {
		return db.AssetClassETF
	}
	if s == "mutual fund" {
		return db.AssetClassMutualFund
	}
	if s == "etp" {
		return db.AssetClassETF
	}
	if strings.Contains(s, "common stock") || strings.Contains(s2, "common stock") ||
		strings.Contains(s, "preferred stock") || strings.Contains(s2, "preferred stock") ||
		strings.Contains(s, "ordinary shares") || strings.Contains(s2, "ordinary shares") ||
		m == "equity" {
		return db.AssetClassStock
	}
	if strings.Contains(m, "govt") || strings.Contains(m, "corp") || strings.Contains(m, "municipal") || strings.Contains(m, "mtge") {
		return db.AssetClassFixedIncome
	}
	if strings.Contains(m, "curncy") || strings.Contains(s, "currency") {
		return db.AssetClassCash
	}
	if log != nil {
		log.Debug("asset class could not be determined from OpenFIGI response",
			"securityType", securityType, "securityType2", securityType2, "marketSector", marketSector)
	}
	return ""
}

// openFIGIResultToInstrument converts one OpenFIGI result to identifier.Instrument and identifiers.
// If the result is a derivative (option/future), underlying is resolved separately and set on inst.
// log is optional; when non-nil and asset class cannot be determined, a debug message is logged.
func openFIGIResultToInstrument(r *OpenFIGIResult, log *slog.Logger) (*identifier.Instrument, []identifier.Identifier) {
	assetClass := assetClassFromOpenFIGI(r.SecurityType, r.SecurityType2, r.MarketSector, log)
	name := r.Name
	if name == "" {
		name = r.SecurityDescription
	}
	if name == "" {
		name = r.Ticker
	}
	inst := &identifier.Instrument{
		AssetClass: assetClass,
		Exchange:   r.ExchCode,
		Currency:   "", // OpenFIGI often omits; leave empty
		Name:       name,
	}
	var ids []identifier.Identifier
	if r.FIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_GLOBAL", Value: r.FIGI})
	}
	if r.ShareClassFIGI != nil && *r.ShareClassFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_SHARE_CLASS", Value: *r.ShareClassFIGI})
	}
	if r.CompositeFIGI != nil && *r.CompositeFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_COMPOSITE", Value: *r.CompositeFIGI})
	}
	if r.Ticker != "" {
		ids = append(ids, identifier.Identifier{Type: "TICKER", Domain: r.ExchCode, Value: identifier.NormalizeSplitTicker(r.Ticker, ".")})
	}
	// OpenFIGI mapping/search response does not include ISIN in the standard fields; skip unless we add a separate lookup
	return inst, ids
}

// isDerivative returns true if the result is an option or future.
func isDerivative(r *OpenFIGIResult) bool {
	ac := assetClassFromOpenFIGI(r.SecurityType, r.SecurityType2, r.MarketSector, nil)
	return ac == db.AssetClassOption || ac == db.AssetClassFuture
}

