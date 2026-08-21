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

// TypeProps are the declared properties of one identifier type.
type TypeProps struct {
	Scope        Scope
	Reassignment Reassignment
}

// idTypes is the controlled vocabulary for identifier types (proto
// IdentifierType names) and their declared properties.
//
// OPENFIGI_GLOBAL is deliberately absent. The venue-specific FIGI it named moved
// to provider_instrument_identifiers, so it is no longer a canonical identifier
// however long the proto enum keeps the member.
var idTypes = map[string]TypeProps{
	// Reassigned only by documented national exception -- a numbering agency
	// reissuing a retired value is rare enough that refusing to chain through
	// these would fragment the master for the sake of an event that happens by
	// exception. All five may mediate a transitive association.
	"ISIN":       {ScopeGlobal, ReassignRare},
	"CUSIP":      {ScopeGlobal, ReassignRare},
	"SEDOL":      {ScopeGlobal, ReassignRare},
	"CINS":       {ScopeGlobal, ReassignRare},
	"WERTPAPIER": {ScopeGlobal, ReassignRare},

	// A FIGI is retired and never reassigned, so it is the one type that would
	// clear a guaranteed-never bar. It sits with the exceptions rather than
	// above them because adr/0061 declines to impose that bar: only a FIGI
	// would pass it, and refusing to chain through ISINs to gain it trades a
	// frequent, certain cost against a rare, correctable one.
	"OPENFIGI_SHARE_CLASS": {ScopeGlobal, ReassignRare},
	"OPENFIGI_COMPOSITE":   {ScopeGlobal, ReassignRare},

	// Tickers are reused constantly and across venues. EA passing from
	// Electronic Arts to whatever holds the symbol now is a live example rather
	// than a hypothetical, and it is global by namespace, which is why scope is
	// the wrong axis for this question.
	"MIC_TICKER":      {ScopeGlobal, ReassignRoutine},
	"OPENFIGI_TICKER": {ScopeGlobal, ReassignRoutine},

	// Contract symbols, from the other direction: a forward split hands one
	// contract's old symbol to the strike below it, and adr/0055 records that
	// this is reachable on most splits rather than on unusual ones.
	"OCC":     {ScopeGlobal, ReassignRoutine},
	"OPRA":    {ScopeGlobal, ReassignRoutine},
	"FUT_OPT": {ScopeGlobal, ReassignRoutine},

	// A currency names what a security trades in, not which security it is, so
	// it fails before reassignment is reached: every instrument denominated in
	// USD would answer to one value. Same ground as BROKER_DESCRIPTION below,
	// reached without the issuer reassigning anything.
	"CURRENCY": {ScopeGlobal, ReassignRoutine},
	"FX_PAIR":  {ScopeGlobal, ReassignRoutine},

	// Not injective at all -- two securities can wear one description.
	"BROKER_DESCRIPTION": {ScopeDescription, ReassignRoutine},

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
