package grouping

import (
	"time"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// ReachKind says which shape of query a Reach is.
type ReachKind int

const (
	// ReachToken is every posting carrying the same correlation token under the
	// same label, answered from the index on (label, token).
	ReachToken ReachKind = iota + 1
	// ReachDates is every posting of one account in a span of time.
	ReachDates
	// ReachOrdinals is every posting of one account whose correlation ordinal falls
	// in a span, under one label.
	ReachOrdinals
)

// Reach is what a rule could link a posting to, expressed so a store can answer it
// from an index.
//
// It is the whole of a rule's predicate that can be turned into a query, not just
// its range component, and the difference matters: closing over "within an ordinal
// span" alone walks an account's entire history, because consecutive references
// chain without end. A rule states the narrowest query that cannot miss a candidate,
// and applies the rest of its predicate in memory.
//
// This is what seeds the neighbourhood closure. Because the closure only ever adds
// postings and is bounded by what the user has, it settles in at most as many steps
// as there are postings -- which is the termination argument the whole design rests
// on. See docs/adr/0050-grouping-recomputes-a-neighbourhood.md.
type Reach struct {
	Kind ReachKind
	// Broker and Account bound every kind: no rule links across users, and none of
	// the rules here links across brokers.
	Broker  typev1.Broker
	Account string
	// AnyAccount widens a reach to the broker, for a correlation whose scope says
	// its token means something outside the account that issued it.
	AnyAccount bool
	// Label names the correlation series, for ReachToken and ReachOrdinals. The
	// empty label is a series like any other.
	Label string
	// Token is the identifier to match, for ReachToken.
	Token string
	// From and Before bound ReachDates, half-open as every interval in this system
	// is (docs/adr/0018-half-open-date-intervals.md).
	From, Before time.Time
	// Low and High bound ReachOrdinals, inclusive at both ends because an ordinal
	// span is stated as a distance rather than as a range.
	Low, High int64
}
