package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// HintsFromTx builds resolution hints from a transaction. The currency hint is
// quotedIn: the line the source stated the security trades on, and never what
// the record settled in.
//
// The security type hint is the asset class the source stated, floored at
// SECURITY. A row is routed to the cash plugins only when its source stated
// cash: an unstated hint, or one saying only that the source does not know,
// left the question open, and the cash plugin then resolved an unidentifiable
// security to its trading currency -- deleting a holding and putting money in
// its place silently, which is the wrong direction to fail in. Every converter
// states a class on its money rows, so the row that loses cash routing here is
// one no converter produces.
//
// Flooring rather than a second field beside it: SECURITY is where the tree
// says "a security of unstated class", so stating it is the whole of what the
// instrument kind used to say, and the routing gate reads one value.
func HintsFromTx(tx *apiv1.Tx) identifier.Hints {
	if tx == nil {
		return identifier.Hints{}
	}
	hint := db.AssetClassSecurity
	if stated := db.AssetClassToStr(tx.GetAssetClassHint()); db.AssetClassClaims(stated) {
		hint = stated
	}
	return identifier.Hints{
		Currency:         quotedIn(tx),
		SecurityTypeHint: hint,
	}
}
