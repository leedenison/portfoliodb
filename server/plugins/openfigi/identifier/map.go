package identifier

import (
	"context"
	"regexp"
	"strings"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// Map OpenFIGI securityType / marketSector to PortfolioDB asset class.
func assetClassFromOpenFIGI(securityType, securityType2, marketSector string) string {
	s := strings.ToLower(securityType)
	s2 := strings.ToLower(securityType2)
	m := strings.ToLower(marketSector)
	if strings.Contains(s2, "option") || strings.Contains(s, "option") {
		return "OPTION"
	}
	if strings.Contains(s2, "future") || strings.Contains(s, "future") {
		return "FUTURE"
	}
	if strings.Contains(s, "etf") || strings.Contains(m, "etf") {
		return "ETF"
	}
	if strings.Contains(s, "mutual") || strings.Contains(s, "fund") {
		return "MF"
	}
	if strings.Contains(m, "equity") || strings.Contains(s, "common stock") || strings.Contains(s2, "common stock") {
		return "EQUITY"
	}
	if strings.Contains(m, "govt") || strings.Contains(m, "corp") || strings.Contains(m, "municipal") || strings.Contains(m, "mtge") {
		return "FIXED_INCOME"
	}
	if strings.Contains(m, "curncy") || strings.Contains(s, "currency") {
		return "CASH"
	}
	return ""
}

// openFIGIResultToInstrument converts one OpenFIGI result to identifier.Instrument and identifiers.
// If the result is a derivative (option/future), underlying is resolved separately and set on inst.
func openFIGIResultToInstrument(r *OpenFIGIResult) (*identifier.Instrument, []identifier.Identifier) {
	assetClass := assetClassFromOpenFIGI(r.SecurityType, r.SecurityType2, r.MarketSector)
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
		ids = append(ids, identifier.Identifier{Type: "FIGI", Value: r.FIGI})
	}
	if r.Ticker != "" {
		ids = append(ids, identifier.Identifier{Type: "SYMBOL", Value: r.Ticker})
	}
	// OpenFIGI mapping/search response does not include ISIN in the standard fields; skip unless we add a separate lookup
	return inst, ids
}

// baseTickerFromOptionTicker extracts the underlying ticker from an option ticker like "IBM 03/20/10 C105" or "AAPL 20250117C200".
var baseTickerRe = regexp.MustCompile(`^([A-Z]{1,5})\s`)

func baseTickerFromOptionTicker(optionTicker string) string {
	optionTicker = strings.TrimSpace(optionTicker)
	if m := baseTickerRe.FindStringSubmatch(optionTicker); len(m) >= 2 {
		return m[1]
	}
	// Fallback: take leading alphabetic run
	for i, r := range optionTicker {
		if r >= '0' && r <= '9' {
			return optionTicker[:i]
		}
	}
	return optionTicker
}

// isDerivative returns true if the result is an option or future.
func isDerivative(r *OpenFIGIResult) bool {
	ac := assetClassFromOpenFIGI(r.SecurityType, r.SecurityType2, r.MarketSector)
	return ac == "OPTION" || ac == "FUTURE"
}

// EnsureUnderlying performs a second OpenFIGI lookup for the underlying and sets inst.Underlying and inst.UnderlyingIdentifiers.
func EnsureUnderlying(ctx context.Context, client *OpenFIGIClient, exchCode string, inst *identifier.Instrument, result *OpenFIGIResult) {
	if !isDerivative(result) {
		return
	}
	baseTicker := baseTickerFromOptionTicker(result.Ticker)
	if baseTicker == "" {
		return
	}
	job := MappingJob{IDType: "TICKER", IDValue: baseTicker}
	if exchCode != "" {
		job.ExchCode = exchCode
	}
	underlyingResults, err := client.Mapping(ctx, job)
	if err != nil || len(underlyingResults) == 0 {
		sr, err2 := client.Search(ctx, baseTicker, exchCode)
		if err2 != nil || sr == nil || len(sr.Data) == 0 {
			return
		}
		for i := range sr.Data {
			if assetClassFromOpenFIGI(sr.Data[i].SecurityType, sr.Data[i].SecurityType2, sr.Data[i].MarketSector) == "EQUITY" {
				underlyingResults = sr.Data[i : i+1]
				break
			}
		}
		if len(underlyingResults) == 0 {
			underlyingResults = sr.Data[:1]
		}
	}
	u := underlyingResults[0]
	underlyingInst, underlyingIds := openFIGIResultToInstrument(&u)
	inst.Underlying = underlyingInst
	inst.UnderlyingIdentifiers = underlyingIds
}
