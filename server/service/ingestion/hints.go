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
func TxTypeToSecurityTypeHint(t apiv1.TxType) string {
	switch t {
	case apiv1.TxType_BUYDEBT, apiv1.TxType_SELLDEBT:
		return identifier.SecurityTypeHintFixedIncome
	case apiv1.TxType_BUYMF, apiv1.TxType_SELLMF:
		return identifier.SecurityTypeHintMutualFund
	case apiv1.TxType_BUYOPT, apiv1.TxType_SELLOPT, apiv1.TxType_CLOSUREOPT:
		return identifier.SecurityTypeHintOption
	case apiv1.TxType_BUYOTHER, apiv1.TxType_SELLOTHER:
		return identifier.SecurityTypeHintUnknown
	case apiv1.TxType_BUYSTOCK, apiv1.TxType_SELLSTOCK, apiv1.TxType_JRNLSEC:
		return identifier.SecurityTypeHintStock
	case apiv1.TxType_BUYFUTURE, apiv1.TxType_SELLFUTURE:
		return identifier.SecurityTypeHintFuture
	case apiv1.TxType_INCOME, apiv1.TxType_INVEXPENSE, apiv1.TxType_REINVEST,
		apiv1.TxType_TRANSFER, apiv1.TxType_MARGININTEREST, apiv1.TxType_RETOFCAP, apiv1.TxType_JRNLFUND,
		apiv1.TxType_CASHFLOW:
		return identifier.SecurityTypeHintCash
	case apiv1.TxType_SPLIT:
		return identifier.SecurityTypeHintUnknown
	default:
		return identifier.SecurityTypeHintUnknown
	}
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

// AssetClassToTxTypes returns all TxType values that map to the given asset class hint.
func AssetClassToTxTypes(assetClass string) []apiv1.TxType {
	var types []apiv1.TxType
	for i := range apiv1.TxType_name {
		t := apiv1.TxType(i)
		if t == apiv1.TxType_TX_TYPE_UNSPECIFIED {
			continue
		}
		if TxTypeToSecurityTypeHint(t) == assetClass {
			types = append(types, t)
		}
	}
	return types
}

// AssetClassToTxTypeStrings returns tx_type DB strings for the given asset class.
func AssetClassToTxTypeStrings(assetClass string) []string {
	types := AssetClassToTxTypes(assetClass)
	strs := make([]string, len(types))
	for i, t := range types {
		strs[i] = t.String()
	}
	return strs
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

