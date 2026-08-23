package ingestion

import (
	"strings"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

// Which of a posting's two currencies a pass wants.
//
// The fields make different claims. trading_currency is the instrument's own
// currency: which line the security is quoted on, and what unit_price is
// denominated in. settlement_currency is what actually paid, which for every
// source in hand is the account's own currency. Either may be absent, and
// absent means nobody said rather than nothing was paid.
//
// So a pass that needs one string has to prefer one, and the preference is not
// a matter of taste -- it follows from the question being asked, and the two
// questions this package asks want opposite answers. Naming them is what keeps
// that legible: a call site says which question it is asking, and the reason it
// prefers what it does lives here rather than being reconstructed from the
// preference itself.

// quotedIn is the currency the source stated the security is quoted in, and
// empty where it stated none.
//
// settlement_currency is deliberately not a fallback. It is what the record
// settled in, which for every source in hand is the account's own currency -- so
// on a security quoted in two it says nothing about which, and is the same as
// saying nothing. Guessing here is the expensive kind of wrong: the value
// filters the OpenFIGI mapping query and is then written onto the instrument as
// its line's currency, and adr/0059 has the resolver counting a matching
// currency as evidence that a guessed identifier found the right security. A
// fabricated currency would narrow the search to the wrong line and then
// corroborate itself. Empty is a better answer than a plausible one: a posting
// that names no line is reported unpriced rather than valued at a rate nobody
// stated (adr/0072).
//
// This is also why the OFX parser stops at an explicit CURSYM rather than
// falling back to the account's CURDEF. The rule here is only ever as good as
// the field feeding it, and by the time an account currency has been written
// into trading_currency upstream, nothing downstream can tell it apart from a
// stated one.
func quotedIn(tx *apiv1.Tx) string {
	return strings.ToUpper(strings.TrimSpace(tx.GetTradingCurrency()))
}

// settleCurrency is the currency a converted weight is denominated in.
// Settlement is what the group is paid in; trading is the fallback for a source
// that reports only the instrument's own denomination.
//
// The fallback quotedIn refuses is safe here because the question is different
// and so is the cost of getting it wrong. This asks what commodity a weight
// cancels in, not which line a security is on, and a group balances only when
// its legs reduce to one commodity -- so a wrong answer shows up as a residual
// the group could not clear, which is loud. A wrong answer in quotedIn is
// silent.
func settleCurrency(tx *apiv1.Tx) string {
	if c := strings.ToUpper(strings.TrimSpace(tx.GetSettlementCurrency())); c != "" {
		return c
	}
	return strings.ToUpper(strings.TrimSpace(tx.GetTradingCurrency()))
}
