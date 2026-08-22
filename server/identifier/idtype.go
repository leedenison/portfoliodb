package identifier

// The properties each identifier type carries, and the vocabulary itself. One
// table, because the vocabulary and what the rules key off it are the same
// question asked twice: a type in one and missing from the other is the drift
// this exists to prevent.
//
// See docs/spec/identifiers.md (Identifier type properties) and
// adr/0061-transitivity-needs-a-non-reassigned-identifier.md.

// Scope says who is the authority for a type's values.
type Scope uint8

const (
	// ScopeUnknown is the zero value and belongs to no type in the table. A
	// lookup that misses reads as this rather than as the first real member, so
	// a type added to the vocabulary and not to the table fails closed.
	ScopeUnknown Scope = iota
	// ScopeGlobal is issued by a registry and validated by identifier plugins.
	ScopeGlobal
	// ScopeBroker is issued by one broker and means nothing without it, so the
	// domain names the broker and only that broker can validate the value.
	ScopeBroker
	// ScopeDescription is the broker's own text for a security, with the
	// ingestion source as domain.
	ScopeDescription
)

// Reassignment says whether the issuer may give one value to a different
// instrument over time.
//
// It is a judgement this system declares on the best evidence available rather
// than a fact anyone can prove, so the evidence is recorded beside each entry
// below and the entry is revisable when a counterexample appears. The line that
// matters is whether reassignment is an exception or a practice: a rare wrong
// link is correctable, where refusing to link at all leaves the security master
// permanently fragmented.
type Reassignment uint8

const (
	// ReassignUnknown is the zero value and belongs to no type in the table,
	// for the reason ScopeUnknown gives.
	ReassignUnknown Reassignment = iota
	// ReassignRare is reassigned by documented exception rather than as a
	// practice, or not at all. The two are one member because adr/0061 puts the
	// line between exception and practice and nothing asks a narrower question:
	// a type retired rather than reissued and a type reassigned once a decade
	// answer every rule here identically, and a member that changed no answer
	// would read as a distinction the rules make.
	ReassignRare
	// ReassignRoutine is reassigned as a matter of course, or does not denote
	// one instrument in the first place.
	ReassignRoutine
)

// Grain says what a type's values name: the security, or one listing of it --
// which is to say one currency the security trades in.
//
// An ISIN spans every listing of a security while a MIC_TICKER names one of
// them. Two listing-grain values are two listings; two security-grain values are
// two names for one thing.
//
// Grain does not imply a domain. A ticker needs one to say which listing it
// names; a SEDOL and a composite FIGI are globally unique without one, as an
// ISIN is at the level above. Where a listing-grain type carries a domain, that
// domain scopes the value, and two values under two named domains are about two
// listings; a security-grain type's domain -- the source that wrote a
// description, the broker that issued a contract number -- names something
// beside the value instead.
//
// Grain also decides where a row is stored: security-grain identifiers against
// the instrument, listing-grain against the listing. It is the axis
// docs/spec/identifiers.md draws when it says metadata is security-level or
// listing-level and the two do not propagate alike.
type Grain uint8

const (
	// GrainUnknown is the zero value and belongs to no type in the table, for
	// the reason ScopeUnknown gives.
	GrainUnknown Grain = iota
	// GrainSecurity names the security itself, however many currencies it
	// trades in.
	GrainSecurity
	// GrainListing names one listing of a security: one currency it trades in.
	// Where the type carries a domain, that domain says which.
	GrainListing
)

// TypeProps are the declared properties of one identifier type.
type TypeProps struct {
	Scope        Scope
	Reassignment Reassignment
	Grain        Grain
}

// idTypes is the controlled vocabulary for identifier types (proto
// IdentifierType names) and their declared properties.
//
// OPENFIGI_GLOBAL is deliberately absent. The venue-specific FIGI it named moved
// to the provider identifiers, where providerIDTypes below carries it as FIGI, so
// it is no longer a canonical identifier however long the proto enum keeps the
// member.
var idTypes = map[string]TypeProps{
	// Reassigned only by documented national exception -- a numbering agency
	// reissuing a retired value is rare enough that refusing to chain through
	// these would fragment the master for the sake of an event that happens by
	// exception. All four may mediate a transitive association.
	"ISIN":       {ScopeGlobal, ReassignRare, GrainSecurity},
	"CUSIP":      {ScopeGlobal, ReassignRare, GrainSecurity},
	"CINS":       {ScopeGlobal, ReassignRare, GrainSecurity},
	"WERTPAPIER": {ScopeGlobal, ReassignRare, GrainSecurity},

	// A FIGI is retired and never reassigned, so it is the one type that would
	// clear a guaranteed-never bar. It sits with the exceptions rather than
	// above them because adr/0061 declines to impose that bar: only a FIGI
	// would pass it, and refusing to chain through ISINs to gain it trades a
	// frequent, certain cost against a rare, correctable one.
	//
	// A share class FIGI is the level above a listing by construction: it is
	// what OpenFIGI issues for the class the individual lines belong to.
	"OPENFIGI_SHARE_CLASS": {ScopeGlobal, ReassignRare, GrainSecurity},

	// Rarely reassigned and listing-grain, which is an uncommon pair. Both still
	// mediate a transitive association, because MayMediate reads reassignment
	// alone and neither of these is reassigned any more freely for naming a line
	// rather than a security.
	//
	// A composite FIGI names a security within a market, and a market's venues
	// share a currency, so what it picks out is the currency line rather than the
	// security above it. A SEDOL is assigned per market and per line, so it lands
	// in the same place by the same argument. Neither carries a domain: both are
	// globally unique without one, which is why grain here says nothing about
	// having one. See adr/0068-a-listing-is-a-currency-of-a-security.md.
	"SEDOL":              {ScopeGlobal, ReassignRare, GrainListing},
	"OPENFIGI_COMPOSITE": {ScopeGlobal, ReassignRare, GrainListing},

	// Tickers are reused constantly and across venues. EA passing from
	// Electronic Arts to whatever holds the symbol now is a live example rather
	// than a hypothetical, and it is global by namespace, which is why scope is
	// the wrong axis for this question.
	//
	// Both name a listing, and the domain is the venue that says which: AAPL on
	// XNAS and AAPL on XLON are two things, not one written twice.
	"MIC_TICKER":      {ScopeGlobal, ReassignRoutine, GrainListing},
	"OPENFIGI_TICKER": {ScopeGlobal, ReassignRoutine, GrainListing},

	// Contract symbols, from the other direction: a forward split hands one
	// contract's old symbol to the strike below it, and adr/0055 records that
	// this is reachable on most splits rather than on unusual ones.
	//
	// Security-grain even so. A contract is its own security -- one set of
	// terms, cleared in one place -- however many venues its underlying trades
	// on, and the symbol carries no venue for a domain to scope.
	"OCC":     {ScopeGlobal, ReassignRoutine, GrainSecurity},
	"OPRA":    {ScopeGlobal, ReassignRoutine, GrainSecurity},
	"FUT_OPT": {ScopeGlobal, ReassignRoutine, GrainSecurity},

	// A currency names what a security trades in, not which security it is, so
	// it fails before reassignment is reached: every instrument denominated in
	// USD would answer to one value. Same ground as BROKER_DESCRIPTION below,
	// reached without the issuer reassigning anything.
	"CURRENCY": {ScopeGlobal, ReassignRoutine, GrainSecurity},
	"FX_PAIR":  {ScopeGlobal, ReassignRoutine, GrainSecurity},

	// Not injective at all -- two securities can wear one description. Its
	// domain is the source that wrote the text rather than a venue, so two
	// sources describing one security are two names for it and not two listings.
	"BROKER_DESCRIPTION": {ScopeDescription, ReassignRoutine, GrainSecurity},

	// ScopeBroker has no members yet. A broker's own contract identifier is the
	// first, and it arrives with 0123.
}

// Props returns the declared properties of an identifier type. The second
// return is false for a type outside the vocabulary.
func Props(t string) (TypeProps, bool) {
	p, ok := idTypes[t]
	return p, ok
}

// Known reports whether a type is in the controlled vocabulary. Candidate
// plugins must return hints whose Type is one of these; anything else is
// discarded.
func Known(t string) bool {
	_, ok := idTypes[t]
	return ok
}

// NamesAListing reports whether a type's values name one listing of a security,
// so that a domain scopes the value rather than naming something beside it.
//
// False for a type outside the vocabulary: an unknown type's domain could be
// anything, and reading it as a venue is the assumption that would have to be
// justified.
func NamesAListing(t string) bool {
	p, ok := idTypes[t]
	return ok && p.Grain == GrainListing
}

// providerIDTypes is the grain of each provider-specific identifier type.
//
// A provider type is a free-form string a plugin invents rather than a member of
// a controlled vocabulary, so this is a table of the ones that exist rather than
// of the ones that are permitted, and it declares grain alone. Scope and
// reassignment are not asked of a provider identifier: it never mediates an
// association and is never admitted as an identity claim, so neither property
// decides anything and declaring them would be inventing answers.
//
// All three name a listing. A segment MIC and a ticker name one venue's line of
// a security; an EODHD exchange code names a market, whose venues share a
// currency; and a venue-specific FIGI is issued per line rather than per
// security, which is what distinguishes it from the share class FIGI above.
var providerIDTypes = map[string]Grain{
	"SEGMENT_MIC_TICKER": GrainListing,
	"EODHD_EXCH_CODE":    GrainListing,
	"FIGI":               GrainListing,
}

// ProviderNamesAListing reports whether a provider-specific identifier type's
// values name one listing of a security.
//
// False for a type outside the table, which is the safe reading rather than the
// likely one: an undeclared provider type could name either level, and filing it
// against the security attaches it to a row that certainly exists, where filing
// it against a listing would have to pick one. A plugin returning a new type
// declares it here.
func ProviderNamesAListing(t string) bool {
	return providerIDTypes[t] == GrainListing
}

// MayMediate reports whether an association on this identifier type may mediate
// a transitive identity claim.
//
// systemOwned is the caller's half of the test and has no default: a user-owned
// association mediates nothing whatever its type, because identifier rows are
// owner-scoped while instruments are not, so a chain drawn through one would
// merge instance-global rows on the strength of one unauthenticated file. The
// two conditions are asked here together rather than in two places, because an
// implementation that reads them apart will eventually read only one.
//
// The third condition -- that the association's two halves have overlapping
// validity intervals -- is not here. It is a fact about two rows rather than
// about a type, so it stays with whoever holds the rows.
//
// Nothing calls this until 0140, which is the rule it exists for.
func MayMediate(t string, systemOwned bool) bool {
	if !systemOwned {
		return false
	}
	p, ok := idTypes[t]
	if !ok {
		return false
	}
	return p.Reassignment == ReassignRare
}
