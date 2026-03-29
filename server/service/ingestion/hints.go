package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// TxTypeStored returns whether transactions of this type are stored. When false (e.g. SPLIT), the transaction is dropped before resolution.
func TxTypeStored(t apiv1.TxType) bool {
	switch t {
	case apiv1.TxType_SPLIT:
		return false
	default:
		return true
	}
}

// TxTypeToSecurityTypeHint maps transaction type to the security type hint vocabulary (identifier package constants).
// Delegates to db.TxTypeToAssetClass since the vocabularies are identical.
func TxTypeToSecurityTypeHint(t apiv1.TxType) string {
	return db.TxTypeToAssetClass(t)
}

// HintsFromTx builds resolution hints from a transaction. Only trading_currency is passed as the currency hint to plugins (never settlement_currency).
func HintsFromTx(tx *apiv1.Tx) identifier.Hints {
	if tx == nil {
		return identifier.Hints{}
	}
	return identifier.Hints{
		Currency:         tx.GetTradingCurrency(),
		SecurityTypeHint: TxTypeToSecurityTypeHint(tx.GetType()),
	}
}

// AssetClassToTxTypeStrings returns tx_type DB strings for the given asset class.
// Delegates to db.AssetClassToTxTypeStrings.
func AssetClassToTxTypeStrings(assetClass string) []string {
	return db.AssetClassToTxTypeStrings(assetClass)
}

// TxIgnored returns whether a transaction should be ignored based on the user's ignore rules.
func TxIgnored(tx *apiv1.Tx, broker string, ignored []db.IgnoredAssetClass) bool {
	hint := TxTypeToSecurityTypeHint(tx.GetType())
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

