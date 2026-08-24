/**
 * Which of a posting's two currencies a reader wants.
 *
 * The fields make different claims. `tradingCurrency` is the instrument's own
 * currency: which line the security is quoted on, and what `unitPrice` is
 * denominated in. `settlementCurrency` is what actually paid, which for every
 * source in hand is the account's own currency. Either may be absent, and absent
 * means nobody said rather than nothing was paid -- an OFX record with no
 * CURRENCY element states no line, and `client/lib/ofx/parser.ts` leaves the
 * field unset rather than filling it with the account default.
 *
 * So a reader that needs one string has to prefer one, and the preference
 * follows from the question being asked rather than from taste. The two
 * questions asked on this side of the wire want opposite answers, which is why
 * each is named: a call site says which one it is asking, and the reason it
 * prefers what it does lives here.
 *
 * The server asks a third question these deliberately cannot answer -- which
 * currency line a security is on -- and answers it from `tradingCurrency` with
 * no fallback at all. See `quotedIn` in `server/service/ingestion/currency.go`.
 *
 * Kept free of React so the import extension can use it.
 */

/** A posting or transaction, from either the archive or the API schema. */
type Denominated = {
  tradingCurrency?: string;
  settlementCurrency?: string;
};

/**
 * What the figures on this row are denominated in, and empty where nothing said.
 *
 * `unitPrice` is stated in `tradingCurrency`, so that is the answer wherever the
 * source named it. Where it did not, the figures are in what the record settled
 * in -- which is exactly how they were read: the OFX parser quotes every figure
 * in CURSYM where the record has one and in the account's CURDEF otherwise, and
 * the second of those reaches us as the settlement currency alone.
 *
 * This is a claim about the numbers on the row, not about the security. It does
 * not say the instrument is quoted in what it returns, and a surface that means
 * to say which line a holding sits on cannot get that from here.
 *
 * Which of the two answered is worth disclosing where a reader could take it for
 * the other. The transaction list shows this beside the price under a heading
 * that says it is what the figures are in, and marks a cell that fell back to
 * settlement -- an empty `tradingCurrency` is what says it did -- because a
 * reader reconciling a statement reads that column as the security's currency.
 */
export function figureCurrency(tx: Denominated): string {
  return tx.tradingCurrency || tx.settlementCurrency || "";
}

/**
 * What a cash leg beside this posting is denominated in, and empty where nothing
 * said.
 *
 * Settlement is what the record paid in; trading is the fallback for a source
 * that reports only the instrument's own denomination. A leg minted with this
 * has to cancel against the posting it was derived beside, so it has to agree
 * with what the server weighs that group in -- `settleCurrency` in
 * `server/service/ingestion/currency.go`, which is this same rule. The two are
 * one rule at two ends: disagreeing would leave every netted fee and
 * reinvestment as a residual in a commodity nothing else uses.
 *
 * The code is returned as the source spelled it. The server upper-cases when it
 * builds the commodity a weight cancels in, where an exact match is what makes
 * two legs cancel; here it is a field the server normalises later.
 */
export function settlesIn(tx: Denominated): string {
  return tx.settlementCurrency || tx.tradingCurrency || "";
}
