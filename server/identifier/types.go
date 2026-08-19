package identifier

import (
	"strings"
	"time"
)

// Asset class must be one of: STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN.
// When AssetClass is OPTION or FUTURE, UnderlyingIdentifiers should be set so the
// resolution layer can resolve the underlying through the full plugin pipeline.

// Instrument holds canonical security-master data for an instrument.
// Identification plugins return enough data to find or create this in the DB.
type Instrument struct {
	ID         string // UUID; may be empty when creating new
	AssetClass string // one of STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN
	Venue      Venue  // where the provider said the instrument trades
	Currency   string
	Name       string // optional display name

	CIK     string // SEC Central Index Key (optional)
	SICCode string // SIC industry classification code (optional)

	// When this instrument is a derivative, plugins provide identifier hints for the
	// underlying. The resolution layer resolves the underlying through the full
	// plugin pipeline using these hints.
	UnderlyingIdentifiers []Identifier

	// Provider-specific identifiers returned by identifier plugins.
	ProviderIdentifiers []ProviderIdentifier

	// Optional: the half-open [ValidFrom, ValidBefore) interval the instrument
	// was available to trade on the exchange in. No plugin supplies these yet.
	ValidFrom   *time.Time
	ValidBefore *time.Time
}

// Venue is what a provider said about where an instrument trades. It is one
// answer at one of two precisions, not two independent facts.
//
// A provider names either a venue or a market. Naming a venue gives an
// operating MIC. Naming a market gives a composite exchange code -- OpenFIGI's
// US, EODHD's US -- which says the security trades somewhere in that country
// without saying where, and so has no MIC at all. Both are knowledge; only the
// first can be stored as an exchange.
//
// The market is held as an ISO 3166 country rather than as the venues the
// composite lists, because the providers disagree about that list and agree
// about the country: OpenFIGI spells its US composite as thirteen operating
// MICs and EODHD spells its as three, so comparing member lists would have
// EODHD's narrower one reject a perfectly good BATS listing. Of the 168
// composites OpenFIGI publishes, 165 are exactly one country's venues.
//
// Nothing stores a Venue. It exists so one provider's answer can be checked
// against another's, which is what stops a London listing being adopted onto a
// security a provider placed in the United States.
type Venue struct {
	MIC     string // ISO 10383 operating MIC, when a venue was named
	Country string // ISO 3166 country code, when a market was named instead
}

// Known reports whether the provider said anything about where it trades.
func (v Venue) Known() bool { return v.MIC != "" || v.Country != "" }

// Permits reports whether mic is consistent with what this answer said.
//
// It is deliberately generous at the edges. An answer that named nothing
// permits everything, because silence contradicts nothing; and an unknown mic
// is permitted by anything, for the same reason. countryOf resolves a MIC to
// its country and may be nil, in which case a market-level answer can only be
// checked against another answer's country.
func (v Venue) Permits(mic string, countryOf func(string) string) bool {
	if !v.Known() || mic == "" {
		return true
	}
	if v.MIC != "" {
		return strings.EqualFold(v.MIC, mic)
	}
	if countryOf == nil {
		return true
	}
	c := countryOf(mic)
	if c == "" {
		return true
	}
	return strings.EqualFold(v.Country, c)
}

// Agrees reports whether two answers can describe the same listing. Each is
// checked against the other, so a market-level answer and a venue-level one are
// compared at whichever precision they share.
func (v Venue) Agrees(other Venue, countryOf func(string) string) bool {
	if !v.Permits(other.MIC, countryOf) || !other.Permits(v.MIC, countryOf) {
		return false
	}
	if v.Country != "" && other.Country != "" {
		return strings.EqualFold(v.Country, other.Country)
	}
	return true
}

// Identifier is an opaque (type, domain, value) for an instrument (e.g. CUSIP, ISIN, MIC_TICKER+MIC, broker description).
// Domain is optional. For MIC_TICKER, domain is an ISO 10383 MIC code (empty when unknown).
// For OPENFIGI_TICKER, domain is a Bloomberg/OpenFIGI exchange code (e.g. "US").
// Broker descriptions use Type = source, Domain = "", Value = full instrument_description.
type Identifier struct {
	Type   string // e.g. "CUSIP", "ISIN", "MIC_TICKER", "OPENFIGI_TICKER"
	Domain string // optional; MIC for MIC_TICKER, exchange code for OPENFIGI_TICKER
	Value  string
}

// ProviderIdentifier is a provider-specific identifier returned by identifier
// plugins. These are stored separately from canonical identifiers and used
// when fetching prices or events from the originating provider.
type ProviderIdentifier struct {
	Provider string // e.g. "massive", "eodhd", "openfigi"
	Type     string // e.g. "SEGMENT_MIC_TICKER", "EODHD_EXCH_CODE", "FIGI"
	Domain   string // optional
	Value    string
}
