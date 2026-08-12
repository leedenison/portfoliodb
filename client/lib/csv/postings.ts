/**
 * The extra postings a converter reads out of one source record.
 *
 * A broker record is not always one posting. A trade's commission is folded into
 * the total rather than reported beside it, and a reinvestment buys units with
 * income no row is written for. Both are read out of the record and neither is
 * recoverable afterwards -- the commission column and the fact that a purchase was
 * funded by income are consumed here -- so the converter emits them.
 *
 * A leg built here is a leg of the record it was built beside, and carries that
 * record's correlations so the server can put the two back together from evidence
 * alone. Saying "these postings transcribe one record" is a fact about the
 * transcription; it is not the inference
 * docs/adr/0041-server-owns-transaction-grouping.md takes away, which is saying
 * that two records are one event.
 *
 * The other side of a one-sided cash event is not here. A dividend's income and a
 * charge's expense follow from the declared type and nothing else, so the server
 * derives them from the posting it already has and recreates them whenever the
 * group changes. See residual.Boundary in server/residual/residual.go and
 * docs/adr/0022-typed-per-account-cash-flow-boundary.md.
 *
 * Kept free of React so the import extension can use it.
 */

import { clone, create } from "@bufbuild/protobuf";
import { Big } from "@/lib/decimal";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";
import type { Correlation, Posting } from "@/gen/archive/v1/txs_pb";
import { CorrelationSchema, PostingSchema } from "@/gen/archive/v1/txs_pb";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";

/**
 * Smallest fee worth a posting, in the settlement currency.
 *
 * Below this the server routes nothing (moneyTolerance in
 * server/service/ingestion/balance.go), so a sub-cent commission would add two
 * postings to every trade and change no balance.
 */
export const FEE_EPSILON = new Big("0.005");

/**
 * The hint that resolves a posting to a currency rather than to a holding named
 * after its description.
 */
export function currencyHint(currency: string) {
  return create(InstrumentRefSchema, {
    type: IdentifierType.CURRENCY,
    value: currency,
  });
}

/**
 * A money posting derived beside one the source reported, in the same group.
 *
 * The instrument description is the currency code, matching how an ordinary cash
 * row arrives, so nothing downstream has to treat a derived posting specially.
 *
 * It carries the correlations of the posting it was derived beside, because it is
 * another leg of that same record: the fee a broker netted into a trade's total and
 * the income a reinvestment consumed are both read out of the record the source
 * wrote, and neither has an identifier of its own to be found by.
 */
function moneyLeg(from: Posting, types: TxType[], accountType: AccountType, quantity: Big): Posting {
  const currency = from.settlementCurrency || from.tradingCurrency;
  return create(PostingSchema, {
    ...(from.timestamp ? { timestamp: clone(TimestampSchema, from.timestamp) } : {}),
    correlations: from.correlations.map((c) => clone(CorrelationSchema, c)),
    instrumentDescription: currency,
    brokerTxType: types,
    assetClassHint: AssetClass.CASH,
    quantity: quantity.toString(),
    unitPrice: "1",
    account: from.account,
    accountType,
    ...(currency
      ? {
          tradingCurrency: currency,
          settlementCurrency: currency,
          identifierHints: [currencyHint(currency)],
        }
      : {}),
  });
}

/**
 * The cash posting for a commission a broker folded into a trade's total.
 *
 * `fee` is the charge as the broker reports it, positive; the posting is
 * negative because the money leaves the account. Its expense leg is the server's,
 * derived from the declared type as it is for a charge the broker reported as a row
 * of its own, so the two end up identical. Returns undefined below FEE_EPSILON.
 */
export function feeLeg(from: Posting, fee: Big | undefined): Posting | undefined {
  if (fee === undefined || fee.abs().lt(FEE_EPSILON)) return undefined;
  return moneyLeg(from, [TxType.TRANSACTION_COST], AccountType.USER, fee.abs().times(-1));
}

/**
 * The income a reinvestment consumed.
 *
 * A reinvestment increases a holding with no cash row beside it: the income
 * buys the units without ever arriving as money, so there is nothing for the
 * source to have reported and nothing to pair with. It is not a sign flip of the
 * posting it balances: that one's quantity is a share count, and what balances it is
 * money.
 *
 * The converter calls this for the rows it knows are reinvestments, and it has to,
 * because nothing on the posting says so -- the units are declared TRADE_ASSET, which
 * is what they are, so the server's boundary rule sees an ordinary purchase and names
 * no income. "Reinvest" was a compressed two-event group rather than a kind of event,
 * and this is the second event.
 *
 * The money is the posting's own weight, quantity times unit price, rather than
 * the total the broker printed on the row. The two differ by the rounding in the
 * quoted price, and taking the broker's would leave the group short by a residual
 * of our choosing rather than one the source has.
 */
export function reinvestIncomeLeg(from: Posting): Posting | undefined {
  const value = new Big(from.quantity).times(from.unitPrice || "0");
  if (value.abs().lt(FEE_EPSILON)) return undefined;
  return moneyLeg(from, [TxType.DIVIDEND], AccountType.INCOME, value.times(-1));
}

/**
 * The label a synthesised record identifier is correlated under.
 *
 * Distinct from every label a broker issues under, so a synthesised token can
 * never be compared against a transcribed one. Nothing but this file writes it,
 * and it means only "these postings came out of the same record".
 */
export const RECORD_LABEL = "record";

/**
 * The correlation that holds the legs of one record together when the source
 * identified the record by nothing.
 *
 * A converter reads several postings out of one row -- a reinvestment's units and
 * the income they consumed, a trade and the commission netted into its total --
 * and the server has to be able to put them back together from what a posting
 * carries. Where the source states a reference the legs share that, and where it
 * states none this stands in for it.
 *
 * Scoped to the file, because it identifies nothing outside the one this converter
 * was handed, and equality only: the token is a position in that file rather than
 * a number a broker issued, so an ordering read out of it would mean nothing.
 *
 * It says the postings came from one record and nothing more. That is a fact about
 * the transcription rather than the inference
 * docs/adr/0041-server-owns-transaction-grouping.md moves to the server, which is
 * the claim that two separate records are legs of one event.
 */
export function recordCorrelation(token: string): Correlation {
  return create(CorrelationSchema, {
    label: RECORD_LABEL,
    token,
    scope: Scope.FILE,
    match: [Match.EXACT],
  });
}

/**
 * Gives a posting a synthesised record identifier if its source gave it none, so
 * that the legs derived beside it inherit something to be found by. Returns the
 * posting for the caller to derive from.
 */
export function identifyRecord(p: Posting, token: string): Posting {
  if (p.correlations.length === 0) p.correlations = [recordCorrelation(token)];
  return p;
}
