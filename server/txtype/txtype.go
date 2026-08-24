// Package txtype holds the transaction type hierarchy: which TxType is under
// which, what a declared set of candidates means, and how a set resolves to the
// single value everything downstream reads. The tree is written here and in
// client/lib/tx-type.ts, and both are checked against testdata/tree.json so the
// two spellings cannot drift.
//
// The package exposes subtree predicates and no bare equality, because the two
// expansions differ -- may-be includes a node's ancestors, must-be does not --
// and either default is silently wrong, one dropping rows from a report and the
// other double-counting. Every call site states which question it is asking.
// See docs/adr/0044-tx-type-is-declared-and-resolved.md.
//
// No query filters on a tx type today, so the SQL expansion this hierarchy
// implies (a subtree expanded to names, passed as resolved_tx_type = ANY($1))
// has no home yet; it belongs here when server-side grouping needs it.
package txtype

import (
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// parent maps every value to the node above it. AMBIGUOUS is the root: it is
// the resolved spelling of "could be any branch", so making it the common
// ancestor of the four families is what lets Resolve fall out of one rule with
// no special case. It is never legal in a declared set.
var parent = map[typev1.TxType]typev1.TxType{
	typev1.TxType_TRADE:             typev1.TxType_AMBIGUOUS,
	typev1.TxType_TRADE_ASSET:       typev1.TxType_TRADE,
	typev1.TxType_TRADE_CASH:        typev1.TxType_TRADE,
	typev1.TxType_INCOME:            typev1.TxType_AMBIGUOUS,
	typev1.TxType_DIVIDEND:          typev1.TxType_INCOME,
	typev1.TxType_INTEREST:          typev1.TxType_INCOME,
	typev1.TxType_RETURN_OF_CAPITAL: typev1.TxType_INCOME,
	typev1.TxType_EXPENSE:           typev1.TxType_AMBIGUOUS,
	typev1.TxType_TRANSACTION_COST:  typev1.TxType_EXPENSE,
	typev1.TxType_HOLDING_COST:      typev1.TxType_EXPENSE,
	typev1.TxType_FINANCING_COST:    typev1.TxType_EXPENSE,
	typev1.TxType_TRANSFER:          typev1.TxType_AMBIGUOUS,
	typev1.TxType_TRANSFER_INTERNAL: typev1.TxType_TRANSFER,
	typev1.TxType_TRANSFER_EXTERNAL: typev1.TxType_TRANSFER,
	typev1.TxType_TRANSFER_LISTING:  typev1.TxType_TRANSFER,
	typev1.TxType_AMBIGUOUS:         typev1.TxType_TX_TYPE_UNSPECIFIED,
}

// Parent returns the node above t, TX_TYPE_UNSPECIFIED above the root.
func Parent(t typev1.TxType) typev1.TxType {
	return parent[t]
}

// under reports whether t is x or lies in x's subtree.
func under(t, x typev1.TxType) bool {
	for t != typev1.TxType_TX_TYPE_UNSPECIFIED {
		if t == x {
			return true
		}
		t = parent[t]
	}
	return false
}

// MustBe reports whether the posting is one of xs under every reading: every
// member of the declared set lies in some x's subtree. This is the predicate a
// rule fires on, so an ambiguous row is not treated as any one of its
// candidates. False for an empty set, which validation rejects before any rule
// runs.
func MustBe(set []typev1.TxType, xs ...typev1.TxType) bool {
	if len(set) == 0 {
		return false
	}
	for _, t := range set {
		ok := false
		for _, x := range xs {
			if under(t, x) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// MayBe reports whether the posting could be one of xs under some reading: some
// member of the declared set lies in an x's subtree, or is an ancestor of an x
// -- a source that said INCOME may yet turn out to have meant DIVIDEND.
func MayBe(set []typev1.TxType, xs ...typev1.TxType) bool {
	for _, t := range set {
		for _, x := range xs {
			if under(t, x) || under(x, t) {
				return true
			}
		}
	}
	return false
}

// ResolvedMustBe is MustBe over the single resolved value: t lies in some x's
// subtree. A resolved internal node answers no -- INCOME is not known to be a
// DIVIDEND.
func ResolvedMustBe(t typev1.TxType, xs ...typev1.TxType) bool {
	return MustBe([]typev1.TxType{t}, xs...)
}

// ResolvedMayBe is MayBe over the single resolved value.
func ResolvedMayBe(t typev1.TxType, xs ...typev1.TxType) bool {
	return MayBe([]typev1.TxType{t}, xs...)
}

// Resolve returns the nearest common ancestor of the set: the member itself for
// a singleton, the shared branch node for siblings, AMBIGUOUS for a
// cross-branch set, TX_TYPE_UNSPECIFIED for an empty one. Until grouping
// resolves for real this is what resolved_tx_type is set to at ingest; it keeps
// whatever was known rather than discarding it.
func Resolve(set []typev1.TxType) typev1.TxType {
	if len(set) == 0 {
		return typev1.TxType_TX_TYPE_UNSPECIFIED
	}
	anc := set[0]
	for _, t := range set[1:] {
		for !under(t, anc) {
			anc = parent[anc]
		}
	}
	return anc
}

// Antichain reports whether no member of the set is an ancestor of another,
// itself included. {TRANSFER, TRANSFER_INTERNAL} is meaningless -- the ancestor
// already covers the descendant -- and validation rejects it.
func Antichain(set []typev1.TxType) bool {
	for i, a := range set {
		for j, b := range set {
			if i != j && under(b, a) {
				return false
			}
		}
	}
	return true
}
