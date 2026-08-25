// Package assetclass holds the asset class hierarchy: which AssetClass is under
// which, and the two questions a caller can ask about one. The tree is written
// here and in client/lib/asset-class.ts, and both are checked against
// testdata/tree.json so the two spellings cannot drift.
//
// There are two things a comparison of asset classes has to keep apart.
//
// The first is how specific a source could be. A broker statement often cannot
// tell a share from an ETF, and an OFX file has no ETF type at all, so a value
// meaning "one of these" has to exist or every source is made to pick one and
// the guess is then indistinguishable from knowledge. That is what the internal
// nodes are for: a leaf is the specificity the system acts on, an internal node
// is what a less specific source says, and both are legal values. Plugins say
// what they mean the same way -- a provider that classified a security only as
// far as a derivative says DERIVATIVE.
//
// The second is whether a given check is strict or permissive, and it does not
// follow from the values. Deciding whether a plugin should be tried is
// permissive: excluding a row because we know little about it loses the row,
// where trying a plugin that turns out not to cover it costs a lookup. Deciding
// whether a source corroborates a plugin's answer is strict: EQUITY does not
// corroborate ETF, because it was never a claim about which of the three.
//
// So the package exposes MustBe and MayBe and no bare equality, as
// [github.com/leedenison/portfoliodb/server/txtype] does for transaction types
// and for the same reason: either default is silently wrong, one refusing rows
// a source was honest about and the other reading a coarse value as a specific
// one. Every call site states which question it is asking.
//
// See docs/adr/0013-security-type-hint-vs-asset-class.md.
package assetclass

import (
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// parent maps every value to the node above it. UNKNOWN is the root: it is what
// a source says when it does not know whether a row is money or a security, so
// making it the common ancestor of the two is what lets the predicates below
// fall out of one rule with no special case.
//
// It is never a routing hint. A posting that made no claim routes as SECURITY,
// because a hint of the root would open the cash plugins to a row nobody said
// was cash and an unidentifiable security would resolve to its trading currency.
var parent = map[typev1.AssetClass]typev1.AssetClass{
	typev1.AssetClass_UNKNOWN:  typev1.AssetClass_ASSET_CLASS_UNSPECIFIED,
	typev1.AssetClass_CASH:     typev1.AssetClass_UNKNOWN,
	typev1.AssetClass_SECURITY: typev1.AssetClass_UNKNOWN,

	// A shareholding, direct or pooled. The three leaves are told apart by how
	// the holding is structured rather than by anything a statement line shows,
	// which is why sources land here so often.
	typev1.AssetClass_EQUITY:      typev1.AssetClass_SECURITY,
	typev1.AssetClass_STOCK:       typev1.AssetClass_EQUITY,
	typev1.AssetClass_ETF:         typev1.AssetClass_EQUITY,
	typev1.AssetClass_MUTUAL_FUND: typev1.AssetClass_EQUITY,

	typev1.AssetClass_FIXED_INCOME: typev1.AssetClass_SECURITY,

	// The classes the schema requires an underlying line for. A contract's
	// strike is a price and a price is in a currency, so an OPTION or a FUTURE
	// delivers one currency line of its underlying rather than the security
	// above it. See docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
	typev1.AssetClass_DERIVATIVE: typev1.AssetClass_SECURITY,
	typev1.AssetClass_OPTION:     typev1.AssetClass_DERIVATIVE,
	typev1.AssetClass_FUTURE:     typev1.AssetClass_DERIVATIVE,

	// A pair is an instrument that holds a price, where a CASH instrument is the
	// money itself, so it sits under SECURITY rather than beside CASH.
	// See docs/adr/0006-fx-as-synthetic-instruments.md.
	typev1.AssetClass_FX: typev1.AssetClass_SECURITY,
}

// Parent returns the node above c, ASSET_CLASS_UNSPECIFIED above the root.
func Parent(c typev1.AssetClass) typev1.AssetClass {
	return parent[c]
}

// under reports whether c is x or lies in x's subtree.
func under(c, x typev1.AssetClass) bool {
	for c != typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		if c == x {
			return true
		}
		c = parent[c]
	}
	return false
}

// MustBe reports whether c is one of xs under every reading: c is an x or lies
// in an x's subtree. This is the strict question, and the one a corroboration
// asks -- an internal node answers no, because EQUITY was never a claim about
// which of the three it is.
//
// False for ASSET_CLASS_UNSPECIFIED, which is a field nobody set rather than a
// value.
func MustBe(c typev1.AssetClass, xs ...typev1.AssetClass) bool {
	for _, x := range xs {
		if under(c, x) {
			return true
		}
	}
	return false
}

// MayBe reports whether c could be one of xs under some reading: c lies in an
// x's subtree, or is an ancestor of one -- a source that said EQUITY may yet
// turn out to have meant ETF.
//
// This is the permissive question, and it is symmetric, so !MayBe is exactly
// "these two claims are disjoint" and is what a contradiction test wants: a
// source and a plugin disagree only when neither reading admits the other.
func MayBe(c typev1.AssetClass, xs ...typev1.AssetClass) bool {
	for _, x := range xs {
		if under(c, x) || under(x, c) {
			return true
		}
	}
	return false
}
