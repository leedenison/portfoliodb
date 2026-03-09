package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
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
	case apiv1.TxType_INCOME, apiv1.TxType_INVEXPENSE, apiv1.TxType_REINVEST,
		apiv1.TxType_TRANSFER, apiv1.TxType_MARGININTEREST, apiv1.TxType_RETOFCAP, apiv1.TxType_JRNLFUND:
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
		ExchangeCode:     tx.GetExchangeCodeHint(),
		Currency:         tx.GetTradingCurrency(),
		MIC:              tx.GetMicHint(),
		SecurityTypeHint: TxTypeToSecurityTypeHint(tx.GetType()),
	}
}

