package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// HintsFromTx builds resolution hints from a transaction. Only trading_currency
// is passed as the currency hint to plugins (never settlement_currency). The
// security type hint is the asset class the source stated, and empty when it
// made no claim -- an empty hint constrains nothing, so a hintless row is
// offered to every plugin.
func HintsFromTx(tx *apiv1.Tx) identifier.Hints {
	if tx == nil {
		return identifier.Hints{}
	}
	hint := ""
	kind := ""
	if ac := tx.GetAssetClassHint(); ac != typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		hint = db.AssetClassToStr(ac)
		kind = db.InstrumentKindSecurity
		if ac == typev1.AssetClass_CASH {
			kind = db.InstrumentKindCash
		}
	}
	return identifier.Hints{
		Currency:         tx.GetTradingCurrency(),
		InstrumentKind:   kind,
		SecurityTypeHint: hint,
	}
}

// TxIgnored returns whether a transaction should be ignored based on the user's
// ignore rules, by the asset class the source stated. A hintless row matches no
// rule at ingest; if it resolves to an ignored class it is removed when the
// rules are next applied to stored data.
func TxIgnored(tx *apiv1.Tx, broker string, ignored []db.IgnoredAssetClass) bool {
	if tx.GetAssetClassHint() == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
		return false
	}
	hint := db.AssetClassToStr(tx.GetAssetClassHint())
	for _, rule := range ignored {
		if rule.Broker != broker {
			continue
		}
		if rule.Account != "" && rule.Account != tx.GetAccount() {
			continue
		}
		if rule.AssetClass == hint {
			return true
		}
	}
	return false
}
