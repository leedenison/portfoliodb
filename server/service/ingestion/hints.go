package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// Security type hint strings passed to identifier plugins (e.g. OpenFIGI securityType2).
const (
	SecurityTypeBond               = "Bond"
	SecurityTypeMutualFund         = "Mutual Fund"
	SecurityTypeOption             = "Option"
	SecurityTypeEquity             = "Equity"
	SecurityTypeCash               = "Cash"
	SecurityTypeUnknown            = "Unknown Security Type"
)

// TxTypeToSecurityType maps transaction type to the security type hint vocabulary.
func TxTypeToSecurityType(t apiv1.TxType) string {
	switch t {
	case apiv1.TxType_BUYDEBT, apiv1.TxType_SELLDEBT:
		return SecurityTypeBond
	case apiv1.TxType_BUYMF, apiv1.TxType_SELLMF:
		return SecurityTypeMutualFund
	case apiv1.TxType_BUYOPT, apiv1.TxType_SELLOPT, apiv1.TxType_CLOSUREOPT:
		return SecurityTypeOption
	case apiv1.TxType_BUYOTHER, apiv1.TxType_SELLOTHER:
		return SecurityTypeUnknown
	case apiv1.TxType_BUYSTOCK, apiv1.TxType_SELLSTOCK:
		return SecurityTypeEquity
	case apiv1.TxType_INCOME, apiv1.TxType_INVEXPENSE, apiv1.TxType_REINVEST,
		apiv1.TxType_TRANSFER, apiv1.TxType_MARGININTEREST, apiv1.TxType_RETOFCAP:
		return SecurityTypeCash
	case apiv1.TxType_SPLIT, apiv1.TxType_JRNLFUND, apiv1.TxType_JRNLSEC:
		return SecurityTypeUnknown
	default:
		return SecurityTypeUnknown
	}
}

// HintsFromTx builds resolution hints from a transaction. Only trading_currency is passed as the currency hint to plugins (never settlement_currency).
func HintsFromTx(tx *apiv1.Tx) identifier.Hints {
	if tx == nil {
		return identifier.Hints{}
	}
	return identifier.Hints{
		ExchangeCode: tx.GetExchangeCodeHint(),
		Currency:     tx.GetTradingCurrency(),
		MIC:          tx.GetMicHint(),
		SecurityType: TxTypeToSecurityType(tx.GetType()),
	}
}

// SecurityTypeAcceptable returns whether the given security type is in the plugin's acceptable list.
// If acceptable is nil or empty, all types are accepted.
func SecurityTypeAcceptable(securityType string, acceptable []string) bool {
	if len(acceptable) == 0 {
		return true
	}
	for _, a := range acceptable {
		if a == securityType {
			return true
		}
	}
	return false
}
