// Package residual classifies what a tx group has left over.
//
// A group balances when the stored weights sum to zero per commodity, and every
// non-zero residual is routed to an explicit posting rather than rejected. Which
// account type that posting takes is this package's whole subject: IMBALANCE for
// a leg the source omitted, TRANSFER_CLEARING for the unmatched side of a
// journal, and SOURCE_ROUNDING for a difference small enough to be the source
// disagreeing with itself. See docs/adr/0024-group-balance-is-checked-on-weight.md.
//
// Boundary is the same subject one step earlier. Where a posting's own type names
// the account its other side sits in -- income for a dividend, expense for a charge
// -- that leg is posted before anything is called a residual, so what reaches the
// rules above is what is left after every side the data does name.
//
// It is a package of its own because two paths route residuals and the family rule
// has to be the same on both. The ingest balancer weighs the postings of an upload;
// replace-by-period sums the stored weights of the legs a cut group has left. Whether
// a residual reads as a transfer or as a missing leg must not depend on which of the
// two produced it -- though only the first can read as the source's own rounding, for
// which see SplitType.
package residual

import (
	"strings"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/txtype"
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
// tolerance for the same reason.
var (
	moneyTolerance     = decimal.RequireFromString("0.005")
	commodityTolerance = decimal.New(1, -6)
)

// PriceScaleFloor is the fewest decimal places a source is taken to have quoted a
// security price to, whatever the scale of the figure it wrote.
//
// It has to be assumed, because the precision is not recoverable. Fidelity strips
// trailing zeros in its own download: the sample export writes prices at 0, 1 and 2
// decimal places, with 47.1 and 47.11 in one instrument's series, so a price reading
// 47.1 means 47.10 and its stated scale understates what was quoted. Reading the
// scale off the figure instead inflates the bound tenfold on a 1dp price and a
// hundredfold on a 0dp one, which classifies the same residuals while leaving far
// more room to swallow a leg that really is missing.
//
// A floor rather than a fixed precision, so a source that quotes finer keeps its own:
// the sample IBKR statements run to 6 decimal places and Schwab to 4, and neither is
// loosened by this.
//
// It is a claim about how brokers quote rather than about any row, in the same class
// as the converter's own calibrated constants. A source that genuinely quoted to 1dp
// would get a bound ten times tighter than its error and keep reporting imbalances,
// which fails safe rather than silently.
const PriceScaleFloor = 2

// Tolerance returns the residual below which a difference in this commodity reads
// as the source's own rounding rather than as a leg it omitted.
//
// priceRounding is what the group's own prices could be out by: for each leg whose
// weight was derived by pricing it into the settlement currency, the units it carried
// times half the last digit the price was quoted to. Zero for a group holding no such
// leg, which is every group that moved only money.
//
// Two roundings, because there are two figures. A cash amount written to 2dp is out
// by up to half a cent however large it is, which is the fixed term. A price written
// to 2dp is out by up to half a penny *per unit*, so on 2676 units it is out by
// 10.30 -- and a group balances on weight, which is where that lands. Covering only
// the first reports every large trade as an imbalance.
//
// The sum is a worst case: it assumes every leg's price erred in the same direction.
// So a small missing leg can hide inside the bound of a high-quantity trade, which is
// the cost of not reporting an artefact of the source's own arithmetic as missing
// data. See docs/adr/0026-exact-decimals-bounded-by-closure.md.
func Tolerance(commodity string, priceRounding decimal.Decimal) decimal.Decimal {
	if !strings.HasPrefix(commodity, CurrencyPrefix) {
		// An unpriced leg was never converted, so no price rounding reaches a
		// residual denominated in the security itself.
		return commodityTolerance
	}
	if priceRounding.IsNegative() {
		return moneyTolerance
	}
	return moneyTolerance.Add(priceRounding)
}

// Type returns the account type the residual of amount in commodity is routed to,
// for a group whose postings resolved to the given tx types and whose prices could be
// out by priceRounding.
//
// The tolerance decides the account type, not whether the residual is routed at
// all: suppressing the small ones would leave the group summing to a small
// non-zero value, which is exactly what the balance constraint rejects, and
// dropping them into IMBALANCE alongside genuinely missing legs would throw away
// the one thing already known about them. A sub-tolerance residual on a journal
// is rounding too, so SOURCE_ROUNDING beats the transfer case rather than the
// other way round.
func Type(commodity string, amount, priceRounding decimal.Decimal, resolved []typev1.TxType) typev1.AccountType {
	if amount.Abs().LessThan(Tolerance(commodity, priceRounding)) {
		return typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING
	}
	return family(resolved)
}

// Boundary returns the account a one-sided posting's other side came from or went
// to, and whether it has one.
//
// A broker reports a dividend and a charge as a single cash row: the money is real
// and the other side is not in the ledger at all, so a leg has to be posted for it or
// the group cannot balance. Where that leg goes is a function of the posting's own
// resolved type and nothing else, which is what makes it the server's to derive
// rather than a converter's to emit
// (docs/adr/0022-typed-per-account-cash-flow-boundary.md).
//
// The test is must-be. A posting that is income under every reading gets an income
// leg, and one whose declared set left the question open gets none, because inventing
// a leg for one reading would assert it. Types with no answer here either already
// balance against a leg the source supplied -- a trade against its cash row -- or
// have their other side in a real account elsewhere, which is a transfer and 0068's
// problem rather than a leg to invent.
func Boundary(resolved typev1.TxType) (typev1.AccountType, bool) {
	switch {
	case txtype.ResolvedMustBe(resolved, typev1.TxType_INCOME):
		return typev1.AccountType_ACCOUNT_TYPE_INCOME, true
	case txtype.ResolvedMustBe(resolved, typev1.TxType_EXPENSE):
		return typev1.AccountType_ACCOUNT_TYPE_EXPENSE, true
	default:
		return typev1.AccountType_ACCOUNT_TYPE_UNSPECIFIED, false
	}
}

// SplitType returns the account type the residual of a group cut by a period replace
// is routed to.
//
// It is Type without the tolerance, and the difference is the whole reason it exists.
// A split residual is the value of the legs the replace removed. It is not two figures
// the source rounded differently, so it can be any size at all, and a small one is
// small by coincidence. Calling that SOURCE_ROUNDING would assert something about the
// source that is false, and would file it below the size at which anything looks
// again. See docs/adr/0039-replace-by-period-deletes-postings-not-groups.md.
func SplitType(resolved []typev1.TxType) typev1.AccountType {
	return family(resolved)
}

// family is the part both callers share: a residual on a transfer is value in
// transit awaiting its other side, and anything else is a leg the source did not
// supply. The test is must-be over each posting's resolved type, so a posting
// that may be a transfer but resolved as ambiguous routes to IMBALANCE -- it is
// not a transfer under every reading, and grouping is what settles it.
func family(resolved []typev1.TxType) typev1.AccountType {
	for _, t := range resolved {
		if txtype.ResolvedMustBe(t, typev1.TxType_TRANSFER) {
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
