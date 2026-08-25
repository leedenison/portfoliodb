package identifier

import (
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/plugins/massive/client"
)

// tickerTypes maps the provider's ticker type to an asset class, for the types
// this says something about. The market having been "stocks" is what rules out
// a contract -- the provider files those under a market of their own -- and the
// type is what says which kind of security it is.
var tickerTypes = map[string]string{
	// Shares in a company, however the holding is wrapped: a depositary receipt
	// and a registered share are claims on the company's stock.
	"CS": db.AssetClassStock, "PFD": db.AssetClassStock, "OS": db.AssetClassStock,
	"ADRC": db.AssetClassStock, "ADRP": db.AssetClassStock, "ADRR": db.AssetClassStock,
	"GDR": db.AssetClassStock, "NYRS": db.AssetClassStock,

	// Exchange-traded vehicles. Notes and trusts are told apart from funds by
	// how they are structured rather than by how they are held, and nothing
	// here turns on that.
	"ETF": db.AssetClassETF, "ETN": db.AssetClassETF,
	"ETV": db.AssetClassETF, "ETS": db.AssetClassETF,

	"FUND": db.AssetClassMutualFund, "BASKET": db.AssetClassMutualFund,

	// A warrant, a right and a unit are deliberately absent, along with every
	// type not listed. They are securities in the stocks market and they are not
	// shareholdings, and there is no value here that means "one of those", so
	// they take the answer below rather than being filed as shares.
}

// stockFromTicker maps a Massive ticker overview to an Instrument and identifiers.
// Returns nil if the ticker is not a stock (market != "stocks").
//
// The class comes from the ticker type where the table above knows it, and is
// SECURITY otherwise: a type this does not recognise leaves the market as the
// only evidence, and "stocks" says the security is not a contract rather than
// that it is a share. Answering STOCK there is what filed every ETF the
// provider has a type for and we did not as a share in a company.
func stockFromTicker(r *client.TickerOverviewResult) (*identifier.Instrument, []identifier.Identifier) {
	if strings.ToLower(r.Market) != "stocks" {
		return nil, nil
	}
	assetClass, ok := tickerTypes[strings.ToUpper(strings.TrimSpace(r.Type))]
	if !ok {
		assetClass = db.AssetClassSecurity
	}
	inst := &identifier.Instrument{
		AssetClass: assetClass,
		Listing: identifier.Listing{
			Venue:    identifier.Venue{MIC: r.PrimaryExchange},
			Currency: strings.ToUpper(r.CurrencyName),
		},
		Name:    r.Name,
		CIK:     r.CIK,
		SICCode: r.SICCode,
	}
	if r.PrimaryExchange != "" && r.Ticker != "" {
		inst.ProviderIdentifiers = append(inst.ProviderIdentifiers,
			identifier.ProviderIdentifier{Provider: "massive", Type: "SEGMENT_MIC_TICKER", Domain: r.PrimaryExchange, Value: r.Ticker})
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
		// A contract carries no currency of its own here; the venue is the
		// option's own, never the underlying's.
		Listing: identifier.Listing{Venue: identifier.Venue{MIC: r.PrimaryExchange}},
		Name:    strings.TrimPrefix(r.Ticker, "O:"),
	}
	if r.PrimaryExchange != "" && r.Ticker != "" {
		inst.ProviderIdentifiers = append(inst.ProviderIdentifiers,
			identifier.ProviderIdentifier{Provider: "massive", Type: "SEGMENT_MIC_TICKER", Domain: r.PrimaryExchange, Value: strings.TrimPrefix(r.Ticker, "O:")})
	}
	if r.UnderlyingTicker != "" {
		// PrimaryExchange is the option's venue (BATO, XASE), never the
		// underlying's, so it says nothing about where the underlying trades.
		// What does is that the contract is an OCC one: the underlying is US
		// listed.
		inst.UnderlyingIdentifiers = []identifier.Identifier{
			{Type: "OPENFIGI_TICKER", Domain: identifier.USComposite, Value: r.UnderlyingTicker},
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
