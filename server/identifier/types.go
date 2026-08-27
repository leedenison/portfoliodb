package identifier

import "strings"

// Asset class must be one of: STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN.
// When AssetClass is OPTION or FUTURE, UnderlyingIdentifiers should be set so the
// resolution layer can resolve the underlying through the full plugin pipeline.

// Instrument holds canonical security-master data for an instrument.
// Identification plugins return enough data to find or create this in the DB.
type Instrument struct {
	ID         string // UUID; may be empty when creating new
	AssetClass string // one of STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN
	// The line this answer is about. A provider answers about one listing of a
	// security -- a quote has a currency and comes from somewhere -- so the
	// result carries a security and one of its lines rather than a flat set of
	// fields at two grains.
	Listing Listing
	Name    string // optional display name

	CIK     string // SEC Central Index Key (optional)
	SICCode string // SIC industry classification code (optional)

	// When this instrument is a derivative, plugins provide identifier hints for the
	// underlying. The resolution layer resolves the underlying through the full
	// plugin pipeline using these hints.
	UnderlyingIdentifiers []Identifier

	// Provider-specific identifiers returned by identifier plugins.
	ProviderIdentifiers []ProviderIdentifier
}

// Listing is what a plugin said about one currency line of the security it
// resolved: what the line is quoted in, and where it trades.
//
// The two are not peers. The currency is what identifies the line -- two
// currencies differ by an FX rate and make two non-fungible holdings -- while
// the venue is an attribute of it, because two venues quoting one currency
// differ by a spread. So a result that named a currency has named a line, and
// one that named only a venue may not have.
//
// See docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
type Listing struct {
	Venue    Venue  // where the provider said the line trades
	Currency string // the code the line is quoted in
}

// Identity is what is known about an instrument at one point in resolution,
// split by where it came from.
//
// Stated is what a source said: the identifiers a broker file carried, or a
// converter read out of one. Proposed is what a plugin offered to fill a gap --
// a ticker for a row that had only a CUSIP, a venue for a bare ticker. The two
// are held apart because only the first is evidence.
//
// A proposal is a thing to be tested by the resolution, never an input the
// resolution may lean on. So it never satisfies a database lookup, never causes
// the conflicting-hints error, and is never written back as an identifier. What
// it may do is break a tie: where two plugins both answered and nothing a source
// said separates them, agreeing with a proposal is better than precedence alone.
//
// The split lives in the shape of the call rather than as a flag on Identifier.
// Identifier is also what becomes db.IdentifierInput and gets stored, so a flag
// there fails open -- every producer and every store site would have to remember
// to clear it, and one missed site persists a guess as canonical identity.
//
// Hints holds what a source stated about currency, instrument kind and security
// type. There is no proposed counterpart: a plugin that wants to offer a
// currency offers it as a CURRENCY identifier in Proposed, which is what keeps
// Hints usable as evidence without qualification.
//
// StatedBy is who vouched for Stated. Empty is a source carrying system
// authority -- a plugin naming the underlying of a derivative it resolved, a
// price fetch naming the ticker it holds a bar for -- and a user id is that
// user's upload. It is what lets an association a broker file asserted be told
// from one a plugin asserted, which decides what a merge may do with it. See
// docs/adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md.
//
// It cannot be read off the owner threaded through resolution: that is who the
// resolution is being carried out for, and the underlying recursion passes it
// unchanged while filling Stated with the plugin's own answer. Nor off Stated
// itself, which is a source's statement at three removes -- a file's, a
// plugin's, and a proposal promoted for want of anything better.
type Identity struct {
	Stated   []Identifier
	StatedBy string
	Proposed []Identifier
	Hints    Hints
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

// Named reports whether the provider said anything about where it trades, at
// either precision. The type's own vocabulary: a provider names either a venue
// or a market, and this is whether it named one of them.
//
// Not Known. identifier.Known is membership of the identifier type vocabulary,
// which is an unrelated question asked in this same package.
func (v Venue) Named() bool { return v.MIC != "" || v.Country != "" }

// Permits reports whether mic is consistent with what this answer said.
//
// It is deliberately generous at the edges. An answer that named nothing
// permits everything, because silence contradicts nothing; and an unknown mic
// is permitted by anything, for the same reason. countryOf resolves a MIC to
// its country and may be nil, in which case a market-level answer can only be
// checked against another answer's country.
func (v Venue) Permits(mic string, countryOf func(string) string) bool {
	if !v.Named() || mic == "" {
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
//
// Two provider answers, and only those. Whether a source stated a venue an
// answer landed on is identification.micAmongStated, which compares one MIC
// against stated identifiers rather than two Venue values against each other.
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
// Broker descriptions use Type = "BROKER_DESCRIPTION", Domain = source, Value = full instrument_description.
//
// This is the triple every plugin family speaks: identification plugins return
// them, and the price and corporate event orchestrators narrow their stored
// identifiers to these before a call. The narrowing is the point -- Canonical
// and the validity interval that [db.IdentifierInput] carries are the store's
// business, and a plugin that could read them could act on them.
//
// An absent domain and an empty one are the same thing, so the zero value of
// Domain is the whole representation of "no domain" and nothing needs a pointer
// to say it.
type Identifier struct {
	Type   string // e.g. "CUSIP", "ISIN", "MIC_TICKER", "OPENFIGI_TICKER"
	Domain string // optional; MIC for MIC_TICKER, exchange code for OPENFIGI_TICKER
	Value  string
}

// Key is the identifier as a cache or ledger key.
//
// The separator is a byte no component can contain, which is what makes the
// join injective: under a printable separator ("A", "", "B:C") and ("A", "B",
// "C") would produce one key and two instruments would share a cache entry.
//
// Identifier is comparable, so code keying a Go map should use the struct
// itself and not this. Key is for the places that must hold a string: the
// per-archive resolve cache and the telemetry ledger, which key on the same
// value so the two cannot disagree about what counts as the same instrument.
func (i Identifier) Key() string {
	return i.Type + "\x00" + i.Domain + "\x00" + i.Value
}

// String names the identifier for a human -- a log line, an error, a telemetry
// description. It is not a key: the separator can occur in a value.
func (i Identifier) String() string {
	return i.Type + ":" + i.Domain + ":" + i.Value
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
