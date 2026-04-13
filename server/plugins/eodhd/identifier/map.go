package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/exchangemap"
)

// stockFromSearch maps an EODHD search result to an Instrument and identifiers.
// Returns nil if the result is not a stock type. exchMap may be nil.
func stockFromSearch(r *client.SearchResult, exchMap *exchangemap.ExchangeMap) (*identifier.Instrument, []identifier.Identifier) {
	if !isStockType(r.Type) {
		return nil, nil
	}
	exchange := resolveExchange(r.Exchange, exchMap)
	inst := &identifier.Instrument{
		AssetClass: db.AssetClassStock,
		Exchange:   exchange,
		Currency:   strings.ToUpper(r.Currency),
		Name:       r.Name,
	}
	if r.Exchange != "" {
		inst.ProviderIdentifiers = append(inst.ProviderIdentifiers,
			identifier.ProviderIdentifier{Provider: "eodhd", Type: "EODHD_EXCH_CODE", Value: r.Exchange})
	}
	var ids []identifier.Identifier
	if r.Code != "" {
		ids = append(ids, identifier.Identifier{Type: "MIC_TICKER", Domain: exchange, Value: identifier.NormalizeSplitTicker(r.Code, ".")})
	}
	if r.ISIN != "" {
		ids = append(ids, identifier.Identifier{Type: "ISIN", Value: r.ISIN})
	}
	return inst, ids
}

// resolveExchange maps an EODHD exchange code to the first operating MIC.
func resolveExchange(eodhdCode string, exchMap *exchangemap.ExchangeMap) string {
	if exchMap == nil || eodhdCode == "" {
		return ""
	}
	mics := exchMap.EODHDCodeToMICs(eodhdCode)
	if len(mics) == 0 {
		return ""
	}
	return mics[0]
}

// bestMatch selects the best search result for a stock. It filters to stock
// types, applies an optional exchange hint, and prefers the primary listing.
func bestMatch(results []client.SearchResult, exchangeHint string) *client.SearchResult {
	var candidates []client.SearchResult
	for _, r := range results {
		if !isStockType(r.Type) {
			continue
		}
		if exchangeHint != "" && !strings.EqualFold(r.Exchange, exchangeHint) {
			continue
		}
		candidates = append(candidates, r)
	}
	if len(candidates) == 0 {
		return nil
	}
	for i := range candidates {
		if candidates[i].IsPrimary {
			return &candidates[i]
		}
	}
	return &candidates[0]
}

// isStockType returns true if the EODHD Type field represents a stock.
func isStockType(typ string) bool {
	t := strings.ToLower(typ)
	return t == "common stock" || t == "preferred stock"
}
