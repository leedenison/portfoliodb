package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// HintsFromTx builds resolution hints from a transaction. The currency hint is
// quotedIn: the line the source stated the security trades on, and never what
// the record settled in. The security type hint is the asset class the source
// stated, and empty when it made no claim.
//
// The instrument kind defaults to SECURITY rather than to "anything": a row is
// routed to the cash plugins only when its source stated CASH. An unstated hint
// left both gates open, and the cash plugin then resolved an unidentifiable
// security to its trading currency -- deleting a holding and putting money in
// its place silently, which is the wrong direction to fail in. Every converter
// states CASH on its money rows, so the row that loses cash routing here is one
// no converter produces.
func HintsFromTx(tx *apiv1.Tx) identifier.Hints {
	if tx == nil {
		return identifier.Hints{}
	}
	hint := ""
	kind := db.InstrumentKindSecurity
	if ac := tx.GetAssetClassHint(); ac != typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		hint = db.AssetClassToStr(ac)
		if ac == typev1.AssetClass_CASH {
			kind = db.InstrumentKindCash
		}
	}
	return identifier.Hints{
		Currency:         quotedIn(tx),
		InstrumentKind:   kind,
		SecurityTypeHint: hint,
	}
}
