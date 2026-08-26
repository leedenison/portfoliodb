package identifier

import "strings"

// The properties each identifier type carries, and the vocabulary itself. One
// table, because the vocabulary and what the rules key off it are the same
// question asked twice: a type in one and missing from the other is the drift
// this exists to prevent.
//
// Nothing here says who vouched for a value. That is the level of authority the
// source supplying it carried, any type can arrive at either level, and it is
// recorded as the row's owner rather than declared per type -- which is why
// there is no scope property, scope having been the proxy for it. What a value
// is good for outside the source that stated it is carried by the identifier's
// own domain, whose role Domain below declares.
//
// See docs/spec/identifiers.md (Identifier type properties),
// adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md and
// adr/0061-transitivity-needs-a-non-reassigned-identifier.md.

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
	// ReassignUnknown is the zero value and belongs to no type in the table. A
	// lookup that misses reads as this rather than as the first real member, so
	// a type added to the vocabulary and not to the table fails closed. Every
	// other property's zero value is there for the same reason.
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
// Grain does not imply a domain, and does not say how many lines a value
// reaches either: Domain and Lines below are those questions, declared per type
// beside this one.
//
// Grain decides where a row is stored: security-grain identifiers against the
// instrument, listing-grain against the listing. It is the axis
// docs/spec/identifiers.md draws when it says metadata is security-level or
// listing-level and the two do not propagate alike.
type Grain uint8

const (
	// GrainUnknown is the zero value and belongs to no type in the table, for
	// the reason ReassignUnknown gives.
	GrainUnknown Grain = iota
	// GrainSecurity names the security itself, however many currencies it
	// trades in.
	GrainSecurity
	// GrainListing names one listing of a security: one currency it trades in.
	GrainListing
)

// Domain says what a type's domain does: scope the value, name something beside
// it, or nothing, the type carrying none.
//
// A property of its own because grain does not imply one. A ticker needs a
// domain to say which listing it names; a SEDOL and a composite FIGI are
// globally unique without one, as an ISIN is at the level above.
type Domain uint8

const (
	// DomainUnknown is the zero value and belongs to no type in the table, for
	// the reason ReassignUnknown gives.
	DomainUnknown Domain = iota
	// DomainAbsent carries no domain: the value stands on its own.
	DomainAbsent
	// DomainScopes says which listing the value is about, and the value names no
	// line until it is there. Two values under two named domains are about two
	// listings. Only a listing-grain type can have one -- a security-grain type
	// has no line for a domain to pick out -- which idtype_test.go asserts
	// rather than leaving to the prose.
	DomainScopes
	// DomainBeside names something other than a listing: the source that wrote a
	// description, the broker that issued a contract number. The value is
	// neither more nor less complete for carrying it.
	DomainBeside
)

// Lines says how many of a security's currency lines a value of this type
// reaches.
//
// Not grain restated. A listing-grain value reaches one line by definition, but
// so does a security-grain value whose security has exactly one line: a currency
// is the cash instrument entire, and a contract is cleared in one place. Grain
// decides where a row is stored; this decides whether naming the security has
// left a line still to choose.
//
// See adr/0068-a-listing-is-a-currency-of-a-security.md, which is what makes the
// count askable: a line is a currency, so a security quoted in one currency has
// one line however many venues quote it.
type Lines uint8

const (
	// LinesUnknown is the zero value and belongs to no type in the table, for
	// the reason ReassignUnknown gives.
	LinesUnknown Lines = iota
	// LinesOne reaches one line, so a source stating one has said the last thing
	// that changes where resolution lands.
	LinesOne
	// LinesMany reaches every line the security trades in: the security is
	// named and which of its lines is open. A stated currency closes exactly
	// that gap, which is why this and LinesNone are two members. They answered
	// every rule alike until a currency was allowed to complete an identity, and
	// a rule now tells them apart.
	LinesMany
	// LinesNone names no security, so it has no lines to count and no currency
	// completes it. A broker description is not injective -- two securities can
	// wear one -- so a currency beside it says which line of neither.
	LinesNone
)

// TypeProps are the declared properties of one identifier type.
type TypeProps struct {
	Reassignment Reassignment
	Grain        Grain
	Domain       Domain
	Lines        Lines
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
	//
	// Each is globally unique without a domain, and each maps to every line the
	// security trades in, so naming one leaves which line still to choose.
	"ISIN":       {ReassignRare, GrainSecurity, DomainAbsent, LinesMany},
	"CUSIP":      {ReassignRare, GrainSecurity, DomainAbsent, LinesMany},
	"CINS":       {ReassignRare, GrainSecurity, DomainAbsent, LinesMany},
	"WERTPAPIER": {ReassignRare, GrainSecurity, DomainAbsent, LinesMany},

	// A FIGI is retired and never reassigned, so it is the one type that would
	// clear a guaranteed-never bar. It sits with the exceptions rather than
	// above them because adr/0061 declines to impose that bar: only a FIGI
	// would pass it, and refusing to chain through ISINs to gain it trades a
	// frequent, certain cost against a rare, correctable one.
	//
	// A share class FIGI is the level above a listing by construction: it is
	// what OpenFIGI issues for the class the individual lines belong to, so it
	// reaches every one of them exactly as an ISIN reaches every line of the
	// security. It was once judged to leave a proposal nothing to add, on the
	// argument that a model handed a provider's key could only invent something
	// to go with it -- written when that sentence covered the composite FIGI
	// too, which really does name one line. adr/0068 took the composite out
	// from under it and this is what was left.
	"OPENFIGI_SHARE_CLASS": {ReassignRare, GrainSecurity, DomainAbsent, LinesMany},

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
	// having one, and each names its line outright. See
	// adr/0068-a-listing-is-a-currency-of-a-security.md.
	"SEDOL":              {ReassignRare, GrainListing, DomainAbsent, LinesOne},
	"OPENFIGI_COMPOSITE": {ReassignRare, GrainListing, DomainAbsent, LinesOne},

	// Tickers are reused constantly and across venues. EA passing from
	// Electronic Arts to whatever holds the symbol now is a live example rather
	// than a hypothetical, and it is global by namespace, which is why scope is
	// the wrong axis for this question.
	//
	// Both name a listing, and the domain is what says which: AAPL on XNAS and
	// AAPL on XLON are two things, not one written twice, and a bare AAPL is
	// every listing of that symbol in the world.
	"MIC_TICKER":      {ReassignRoutine, GrainListing, DomainScopes, LinesOne},
	"OPENFIGI_TICKER": {ReassignRoutine, GrainListing, DomainScopes, LinesOne},

	// Contract symbols, from the other direction: a forward split hands one
	// contract's old symbol to the strike below it, and adr/0055 records that
	// this is reachable on most splits rather than on unusual ones.
	//
	// Security-grain even so. A contract is its own security -- one set of
	// terms, cleared in one place -- however many venues its underlying trades
	// on, and the symbol carries no venue for a domain to scope. Cleared in one
	// place is also why the security it names has one line.
	"OCC":     {ReassignRoutine, GrainSecurity, DomainAbsent, LinesOne},
	"OPRA":    {ReassignRoutine, GrainSecurity, DomainAbsent, LinesOne},
	"FUT_OPT": {ReassignRoutine, GrainSecurity, DomainAbsent, LinesOne},

	// A currency names what a security trades in, not which security it is, so
	// it fails before reassignment is reached: every instrument denominated in
	// USD would answer to one value. Same ground as BROKER_DESCRIPTION below,
	// reached without the issuer reassigning anything.
	//
	// The instrument each names is the cash or FX instrument entire, which is
	// one line by construction.
	"CURRENCY": {ReassignRoutine, GrainSecurity, DomainAbsent, LinesOne},
	"FX_PAIR":  {ReassignRoutine, GrainSecurity, DomainAbsent, LinesOne},

	// Not injective at all -- two securities can wear one description. Its
	// domain is the source that wrote the text rather than a venue, so two
	// sources describing one security are two names for it and not two listings.
	// It states no security, so it leaves the identity as incomplete as it found
	// it, and a currency stated beside it completes nothing: it would name a
	// line of whichever security the text turns out to mean.
	"BROKER_DESCRIPTION": {ReassignRoutine, GrainSecurity, DomainBeside, LinesNone},

	// A broker's own contract identifier has no entry yet. It arrives with 0123,
	// which declares its lines; its domain names the broker, so it is
	// DomainBeside. Nothing about it says who vouched for the value: the same
	// number is a fact from a broker feed and a claim from an upload, which is
	// the owner's question and not this table's.
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
//
// A question about an identifier type, and the only one in this package
// spelled this way. Venue.Named asks whether a provider said where a security
// trades, which is the question a reader might expect this name to answer.
func Known(t string) bool {
	_, ok := idTypes[t]
	return ok
}

// namesAListing is the rule NamesAListing and ProviderNamesAListing each apply
// to their own table: a type names a listing when its table declares it
// listing-grain, and a type the table does not declare reads as GrainUnknown,
// which is not GrainListing, so a miss answers no.
//
// One rule and not two. The entry points reach that no by opposite-sounding
// arguments -- a canonical type's unknown domain must not be read as a venue, an
// undeclared provider type must file against the security rather than a listing
// nothing picked -- and one mechanic serves both: a grain nobody declared is not
// a listing. Written once so a third table cannot answer it differently.
func namesAListing(g Grain) bool { return g == GrainListing }

// NamesAListing reports whether a type's values name one listing of a security,
// so that a domain scopes the value rather than naming something beside it.
//
// False for a type outside the vocabulary: an unknown type's domain could be
// anything, and reading it as a venue is the assumption that would have to be
// justified. That is the same rule ProviderNamesAListing applies to the provider
// table, reached from the other direction; namesAListing above holds it.
//
// Not ReachesOneLine, which asks of one identifier whether the value it carries
// picks out a line. A bare MIC_TICKER passes this and fails that.
func NamesAListing(t string) bool { return namesAListing(idTypes[t].Grain) }

// ReachesOneLine reports whether this identifier, with the domain it carries,
// names one currency line of a security -- so that which line is no longer open.
//
// Two halves, one from the table and one from the value, which is why this takes
// an identifier and not a type name. Lines says whether values of this type
// reach one line; Domain says whether this value is one of them, a MIC_TICKER
// carrying its MIC naming a line where a bare one names every listing of that
// symbol in the world.
//
// Not NamesAListing, which asks of a type where its rows are stored: a bare
// MIC_TICKER passes that and fails this, and an OCC fails that and passes this,
// a contract being its own security and having one line. Not
// identification.micStated, which asks whether a source named something a MIC
// can be compared against: an OPENFIGI_TICKER's composite exchange code reaches
// one line here and is not a venue there.
//
// False for a type outside the vocabulary, for the reason NamesAListing gives.
func ReachesOneLine(id Identifier) bool {
	p, ok := idTypes[id.Type]
	if !ok || p.Lines != LinesOne {
		return false
	}
	return p.Domain != DomainScopes || strings.TrimSpace(id.Domain) != ""
}

// NamesTheSecurity reports whether this identifier names the security it belongs
// to and leaves only which of its lines open, so that a currency stated beside
// it completes the identity.
//
// LinesMany exactly. A LinesOne value has already named the line and needs no
// currency, or -- a bare MIC_TICKER -- has named neither the line nor the
// security, tickers being reused across venues, and a currency beside it would
// name the line of no particular security. A LinesNone value named no security
// at all.
//
// The domain does not enter, though this takes an identifier so that it reads
// beside ReachesOneLine at a call site that asks both. No LinesMany type has a
// domain that scopes its value -- they are security-grain, and a security has no
// line for a domain to pick out -- so there is nothing for a domain to decide.
// idtype_test.go asserts it rather than leaving it to this comment.
//
// False for a type outside the vocabulary, for the reason NamesAListing gives.
func NamesTheSecurity(id Identifier) bool {
	p, ok := idTypes[id.Type]
	return ok && p.Lines == LinesMany
}

// CorroboratesSecurity reports whether two results naming one value of this type
// have thereby named one security.
//
// Grain selects it. A ticker, a SEDOL and a composite FIGI name one line, and
// two results agreeing about a line have not said they resolved one security --
// adr/0060 says exactly this of a currency and a venue.
//
// A description is excluded on the other axis: it names no security at all. Two
// securities can wear one description, so two results agreeing on the text have
// agreed about the text rather than about its subject. LinesNone is where the
// table records that -- a type with no security to speak of has no lines to
// count -- so it is what this reads, rather than a second property saying the
// same thing.
//
// Routine reassignment does not exclude a type here, though it bars one from
// mediating a chain (adr/0061). A contract symbol passes to another strike over
// time, but two results resolving now from one symbol both mean today's
// contract. Reassignment is a question about time; this one is about how much a
// query left open at an instant.
//
// False for a type outside the vocabulary, for the reason NamesAListing gives.
//
// See docs/adr/0078-merge-admission-needs-a-security-both-results-named.md.
func CorroboratesSecurity(t string) bool {
	p, ok := idTypes[t]
	return ok && p.Grain == GrainSecurity && p.Lines != LinesNone
}

// providerIDTypes is the grain of each provider-specific identifier type.
//
// A provider type is a free-form string a plugin invents rather than a member of
// a controlled vocabulary, so this is a table of the ones that exist rather than
// of the ones that are permitted, and it declares grain alone. Reassignment,
// domain and lines are not asked of a provider identifier: it never mediates an
// association, is never admitted as an identity claim and is never what a source
// stated, so none of them decides anything and declaring them would be inventing
// answers.
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
// declares it here. That is the same rule NamesAListing applies to the canonical
// table, reached from the other direction; namesAListing above holds it.
func ProviderNamesAListing(t string) bool { return namesAListing(providerIDTypes[t]) }

// MayMediate reports whether a value of this identifier type is stable enough
// over time to carry a transitive identity claim.
//
// One of adr/0061's three conditions and the only one a type can answer. The
// other two are facts about the stored rows: that their validity intervals
// overlap, and that the association is one the system holds as settled rather
// than as a user's claim. Neither is here, because two rows of one type differ
// on both -- the same ISIN is a fact from an identifier plugin and a claim from
// a broker file -- so the caller asks the rows and asks this in the same breath.
//
// What no question about the source reaches is the case this exists for: two
// associations an identifier plugin wrote, both settled, chained through a value
// the issuer handed to something else in between. Only a property of the value
// catches that.
func MayMediate(t string) bool {
	p, ok := idTypes[t]
	if !ok {
		return false
	}
	return p.Reassignment == ReassignRare
}
