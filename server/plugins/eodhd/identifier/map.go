package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/eodhd/client"
)

// stockFromSearch maps an EODHD search result (and optional fundamentals) to
// an Instrument and identifiers. Returns nil if the result is not a stock type.
func stockFromSearch(r *client.SearchResult) (*identifier.Instrument, []identifier.Identifier) {
	if !isStockType(r.Type) {
		return nil, nil
	}
	inst := &identifier.Instrument{
		AssetClass: db.AssetClassStock,
		Exchange:   r.Exchange,
		Currency:   strings.ToUpper(r.Currency),
		Name:       r.Name,
	}
	var ids []identifier.Identifier
	if r.Code != "" {
		ids = append(ids, identifier.Identifier{Type: "TICKER", Domain: r.Exchange, Value: r.Code})
	}
	if r.ISIN != "" {
		ids = append(ids, identifier.Identifier{Type: "ISIN", Value: r.ISIN})
	}
	return inst, ids
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
