package identifier

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

var errUnderlyingNotFound = errors.New("underlying instrument not found")

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

// UnderlyingResolver returns the underlying symbol and optional exchange hint for a derivative ticker.
// Used so that the openfigi plugin can use a separate derivative parsing library and optionally OpenAI
// without assuming the underlying trades on the same exchange as the derivative.
type UnderlyingResolver func(ctx context.Context, derivativeTicker string) (symbol, exchangeHint string, ok bool)

// EnsureUnderlying performs a second OpenFIGI lookup for the underlying and sets inst.Underlying and inst.UnderlyingIdentifiers.
// The resolver (e.g. library + OpenAI) provides the underlying symbol and optional exchange hint; the derivative's exchange is not used.
// Returns errUnderlyingNotFound if the result is a derivative but the underlying could not be resolved.
// Returns nil if the result is not a derivative (no-op) or the underlying was resolved successfully.
func EnsureUnderlying(ctx context.Context, client *OpenFIGIClient, inst *identifier.Instrument, result *OpenFIGIResult, resolve UnderlyingResolver) error {
	if !isDerivative(result) || resolve == nil {
		return nil
	}
	symbol, exchangeHint, ok := resolve(ctx, result.Ticker)
	if !ok || symbol == "" {
		return errUnderlyingNotFound
	}
	job := MappingJob{IDType: "TICKER", IDValue: symbol}
	if exchangeHint != "" {
		job.ExchCode = exchangeHint
	}
	underlyingResults, err := client.Mapping(ctx, job)
	if err != nil || len(underlyingResults) == 0 {
		sr, err2 := client.Search(ctx, symbol, exchangeHint)
		if err2 != nil || sr == nil || len(sr.Data) == 0 {
			return errUnderlyingNotFound
		}
		for i := range sr.Data {
			if assetClassFromOpenFIGI(sr.Data[i].SecurityType, sr.Data[i].SecurityType2, sr.Data[i].MarketSector, nil) == db.AssetClassStock {
				underlyingResults = sr.Data[i : i+1]
				break
			}
		}
		if len(underlyingResults) == 0 {
			underlyingResults = sr.Data[:1]
		}
	}
	u := underlyingResults[0]
	underlyingInst, underlyingIds := openFIGIResultToInstrument(&u, nil)
	if underlyingInst == nil {
		return errUnderlyingNotFound
	}
	inst.Underlying = underlyingInst
	inst.UnderlyingIdentifiers = underlyingIds
	return nil
}
