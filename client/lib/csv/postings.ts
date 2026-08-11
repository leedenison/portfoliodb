/**
 * The postings a converter has to synthesise for a group to balance.
 *
 * Brokers report one-sided events: a dividend or a charge arrives as a single
 * cash row, and a trade's commission is folded into one cash total. Under
 * docs/adr/0021-converters-own-transaction-grouping.md the missing legs are the
 * converter's to emit rather than the server's to infer, so these helpers build
 * them and every converter produces the same shape.
 *
 * Kept free of React so the import extension can use it.
 */

import { clone, create } from "@bufbuild/protobuf";
import { Big } from "@/lib/decimal";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";
import type { Posting } from "@/gen/archive/v1/txs_pb";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { AccountType, AssetClass, IdentifierType, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";

/**
 * Where the other side of a one-sided cash event came from or went to. A
 * dividend's money comes from income and a charge's goes to expense; the broker
 * reports only the cash. The test is must-be over the declared set: a row that
 * is income under every reading gets an income counter-leg, and an ambiguous one
 * gets none, because inventing a leg for one of its readings would assert it.
 * Types with no branch here either already balance against another posting the
 * source supplied (a trade against its cash leg) or have their other side in a
 * different account entirely (a transfer), which is 0068's problem rather than a
 * leg to invent.
 */
function counterAccountType(set: readonly TxType[]): AccountType | undefined {
  if (mustBe(set, TxType.INCOME)) return AccountType.INCOME;
  if (mustBe(set, TxType.EXPENSE)) return AccountType.EXPENSE;
  return undefined;
}

/**
 * Smallest fee worth a posting, in the settlement currency.
 *
 * Below this the server routes nothing (moneyTolerance in
 * server/service/ingestion/balance.go), so a sub-cent commission would add two
 * postings to every trade and change no balance.
 */
export const FEE_EPSILON = new Big("0.005");

/**
 * The mirror of a one-sided cash posting: the same money in the account it came
 * from or went to. Returns undefined when the type has no other side to name, or
 * when the posting is itself a counter-leg.
 */
export function counterLeg(p: Posting): Posting | undefined {
  const accountType = counterAccountType(p.brokerTxType);
  if (accountType === undefined) return undefined;
  if (p.accountType !== AccountType.USER && p.accountType !== AccountType.UNSPECIFIED) {
    return undefined;
  }
  const leg = clone(PostingSchema, p);
  leg.quantity = new Big(p.quantity).times(-1).toString();
  leg.accountType = accountType;
  // A derived leg transcribes no source row, because the source wrote none for
  // it. The clone above would otherwise hand it the evidence of the posting it
  // mirrors, stating that the source correlated a row it never wrote. moneyLeg
  // builds from scratch and needs no such reset. See docs/spec/postings.md.
  leg.correlations = [];
  return leg;
}

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
 */
function moneyLeg(from: Posting, types: TxType[], accountType: AccountType, quantity: Big): Posting {
  const currency = from.settlementCurrency || from.tradingCurrency;
  return create(PostingSchema, {
    ...(from.timestamp ? { timestamp: clone(TimestampSchema, from.timestamp) } : {}),
    instrumentDescription: currency,
    brokerTxType: types,
    assetClassHint: AssetClass.CASH,
    quantity: quantity.toString(),
    unitPrice: "1",
    account: from.account,
    groupRef: from.groupRef,
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
 * negative because the money leaves the account. Its counter-leg comes from
 * counterLeg, so a derived fee and a separately reported one end up identical.
 * Returns undefined below FEE_EPSILON.
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
 * source to have reported and nothing to pair with. Its other side is therefore
 * derived, and unlike counterLeg's it is not a sign flip -- the posting's
 * quantity is a share count, and what balances it is money. The converter calls
 * this for the rows it knows are reinvestments; nothing on the posting itself
 * says so any more, because "reinvest" was a compressed two-event group rather
 * than a kind of event.
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
 * A group_ref prefix no posting in the batch already starts with, so a
 * synthesised ref cannot collide with a broker's own. Mirrors groupPostings in
 * server/service/ingestion/balance.go.
 */
export function refPrefix(postings: Posting[]): string {
  let prefix = "p";
  for (const p of postings) {
    while (p.groupRef?.startsWith(prefix)) prefix += "_";
  }
  return prefix;
}

/**
 * The counter-leg of every one-sided cash posting in the batch, for the caller
 * to append. Postings that had no group_ref are given one in place, since a leg
 * only balances the posting it mirrors if the two share a group.
 */
export function counterLegs(postings: Posting[]): Posting[] {
  const prefix = refPrefix(postings);
  const legs: Posting[] = [];
  postings.forEach((p, i) => {
    const leg = counterLeg(p);
    if (!leg) return;
    if (!p.groupRef) {
      p.groupRef = `${prefix}${i}`;
      leg.groupRef = p.groupRef;
    }
    legs.push(leg);
  });
  return legs;
}
