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
	venue := resolveVenue(r.Exchange, r.Country, exchMap)
	exchange := venue.MIC
	inst := &identifier.Instrument{
		AssetClass: db.AssetClassStock,
		Listing: identifier.Listing{
			Venue:    venue,
			Currency: strings.ToUpper(r.Currency),
		},
		Name: r.Name,
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

// resolveVenue reads an EODHD exchange code as what it says about where the
// instrument trades: an operating MIC when the code names one venue, and the
// country when it does not.
//
// US is the case that matters. EODHD reports it for every American listing and
// its own reference data spells it as XNAS, XNYS and OTCM, so the code cannot
// say which. Taking the first wrote XNAS on NYSE issues and stored it as the
// domain of a canonical MIC_TICKER, which then made a correct XNYS from another
// plugin read as a contradiction and got it discarded. The code itself is still
// recorded, as the EODHD_EXCH_CODE provider identifier.
//
// The country comes from the search result, which states it per listing, so no
// mapping of EODHD's codes to countries is needed.
func resolveVenue(eodhdCode, country string, exchMap *exchangemap.ExchangeMap) identifier.Venue {
	if exchMap != nil && eodhdCode != "" {
		if mics := exchMap.EODHDCodeToMICs(eodhdCode); len(mics) == 1 {
			return identifier.Venue{MIC: mics[0]}
		}
	}
	return identifier.Venue{Country: isoCountry(country)}
}

// isoCountry maps the country names EODHD returns to ISO 3166 codes. It knows
// only the spellings that appear in search results; anything else yields "",
// which constrains nothing rather than constraining wrongly.
func isoCountry(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "USA", "US", "UNITED STATES":
		return "US"
	default:
		return ""
	}
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
