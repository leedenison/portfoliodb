package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

// stockFromTicker maps a Massive ticker overview to an Instrument and identifiers.
// Returns nil if the ticker is not a stock (market != "stocks").
func stockFromTicker(r *client.TickerOverviewResult) (*identifier.Instrument, []identifier.Identifier) {
	if strings.ToLower(r.Market) != "stocks" {
		return nil, nil
	}
	inst := &identifier.Instrument{
		AssetClass: db.AssetClassStock,
		Exchange:   r.PrimaryExchange,
		Currency:   strings.ToUpper(r.CurrencyName),
		Name:       r.Name,
		CIK:        r.CIK,
		SICCode:    r.SICCode,
	}
	ids := tickerIdentifiers(r)
	return inst, ids
}

// optionFromContract maps a Massive options contract to an Instrument and identifiers.
// UnderlyingIdentifiers are populated from underlyingTicker so the resolution layer
// can resolve the underlying through the full plugin pipeline.
func optionFromContract(r *client.OptionsContractResult) (*identifier.Instrument, []identifier.Identifier) {
	inst := &identifier.Instrument{
		AssetClass: db.AssetClassOption,
		Exchange:   r.PrimaryExchange,
		Name:       strings.TrimPrefix(r.Ticker, "O:"),
	}
	if r.UnderlyingTicker != "" {
		inst.UnderlyingIdentifiers = []identifier.Identifier{
			{Type: "MIC_TICKER", Value: r.UnderlyingTicker},
		}
	}
	var ids []identifier.Identifier
	if r.Ticker != "" {
		occVal := strings.TrimPrefix(r.Ticker, "O:")
		ids = append(ids, identifier.Identifier{Type: "OCC", Value: occVal})
		ids = append(ids, identifier.Identifier{Type: "MIC_TICKER", Domain: r.PrimaryExchange, Value: occVal})
	}
	return inst, ids
}

// tickerIdentifiers extracts TICKER and FIGI identifiers from a ticker overview.
func tickerIdentifiers(r *client.TickerOverviewResult) []identifier.Identifier {
	var ids []identifier.Identifier
	if r.Ticker != "" {
		ids = append(ids, identifier.Identifier{Type: "MIC_TICKER", Domain: r.PrimaryExchange, Value: r.Ticker})
	}
	if r.CompositeFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_COMPOSITE", Value: r.CompositeFIGI})
	}
	if r.ShareClassFIGI != "" {
		ids = append(ids, identifier.Identifier{Type: "OPENFIGI_SHARE_CLASS", Value: r.ShareClassFIGI})
	}
	return ids
}
