// Package residual classifies what a tx group has left over.
//
// A group balances when the stored weights sum to zero per commodity, and every
// non-zero residual is routed to an explicit posting rather than rejected. Which
// account type that posting takes is this package's whole subject: IMBALANCE for
// a leg the source omitted, TRANSFER_CLEARING for the unmatched side of a
// journal, and SOURCE_ROUNDING for a difference small enough to be the source
// disagreeing with itself. See docs/adr/0024-group-balance-is-checked-on-weight.md.
//
// It is a package of its own because two paths route residuals and the rule has
// to be the same on both. The ingest balancer weighs the postings of an upload;
// the partial delete in replace-by-period sums the weights already stored. A
// group's residual must not depend on which of the two produced it.
package residual

import (
	"strings"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/shopspring/decimal"
)

// The commodity a weight is contributed in, prefixed by kind. The three are not
// the same kind of thing, so a description that happened to read USD is not the
// same commodity as the currency. See docs/adr/0029-posting-weight-is-stored.md.
const (
	CurrencyPrefix    = "cur:"
	InstrumentPrefix  = "inst:"
	DescriptionPrefix = "desc:"
)

// transferTypes are the journals whose other side is a different account. Their
// residual is an unmatched transfer rather than a data-quality problem, so it is
// routed to TRANSFER_CLEARING and holds the value in transit until the pair is
// matched.
var transferTypes = map[typev1.TxType]bool{
	typev1.TxType_TRANSFER: true,
	typev1.TxType_JRNLFUND: true,
	typev1.TxType_JRNLSEC:  true,
}

// Tolerances below which a residual is the source disagreeing with itself rather
// than a leg it left out. A trade of 37 shares at 12.3456 costs 456.7872 against a
// broker cash row of -456.79: the 0.0028 is an artefact of the source being
// written to 2dp, not a real discrepancy. Beancount infers half the last
// significant digit for exactly this case, and half a cent is what it would infer
// for 2dp money.
//
// Quantities are exact decimals now, so this is no longer absorbing arithmetic
// error -- it is the disagreement between two figures the source rounded
// differently, which exactness does not remove. Both beancount and ledger keep a
// tolerance for the same reason. Inferring it from the scale of the contributing
// amounts, rather than fixing it, is a change to how residuals get classified and
// is deliberately not made here.
var (
	moneyTolerance     = decimal.RequireFromString("0.005")
	commodityTolerance = decimal.New(1, -6)
)

// Tolerance returns the residual below which a difference in this commodity reads
// as the source's own rounding rather than as a leg it omitted.
func Tolerance(commodity string) decimal.Decimal {
	if strings.HasPrefix(commodity, CurrencyPrefix) {
		return moneyTolerance
	}
	return commodityTolerance
}

// Type returns the account type the residual of amount in commodity is routed to,
// for a group whose postings have the given tx types.
//
// The tolerance decides the account type, not whether the residual is routed at
// all: suppressing the small ones would leave the group summing to a small
// non-zero value, which is exactly what the balance constraint rejects, and
// dropping them into IMBALANCE alongside genuinely missing legs would throw away
// the one thing already known about them. A sub-tolerance residual on a journal
// is rounding too, so SOURCE_ROUNDING beats the transfer case rather than the
// other way round.
func Type(commodity string, amount decimal.Decimal, txTypes []typev1.TxType) typev1.AccountType {
	if amount.Abs().LessThan(Tolerance(commodity)) {
		return typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING
	}
	for _, t := range txTypes {
		if transferTypes[t] {
			return typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING
		}
	}
	return typev1.AccountType_ACCOUNT_TYPE_IMBALANCE
}

// CurrencyOf returns the currency code a money commodity names, and whether it is
// one. The routed posting for a money residual needs the code to resolve the
// seeded currency instrument it is denominated in.
func CurrencyOf(commodity string) (string, bool) {
	code, ok := strings.CutPrefix(commodity, CurrencyPrefix)
	return code, ok
}

// InstrumentOf returns the instrument id a security commodity names, and whether
// it is one. A residual in a security carries the instrument of the legs it
// balances rather than resolving one.
func InstrumentOf(commodity string) (string, bool) {
	id, ok := strings.CutPrefix(commodity, InstrumentPrefix)
	return id, ok
}
