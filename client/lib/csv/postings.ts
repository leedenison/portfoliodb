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
import type { Tx } from "@/gen/api/v1/api_pb";
import {
  AccountType,
  IdentifierType,
  InstrumentIdentifierSchema,
  TxSchema,
  TxType,
} from "@/gen/api/v1/api_pb";

/**
 * Where the other side of a one-sided cash event came from or went to. A
 * dividend's money comes from income and a charge's goes to expense; the broker
 * reports only the cash. Types absent here either already balance against
 * another posting the source supplied (a trade against its cash leg) or have
 * their other side in a different account entirely (a journal), which is 0068's
 * problem rather than a leg to invent.
 */
const COUNTER_TYPE = new Map<TxType, AccountType>([
  [TxType.INCOME, AccountType.INCOME],
  [TxType.RETOFCAP, AccountType.INCOME],
  [TxType.INVEXPENSE, AccountType.EXPENSE],
  [TxType.MARGININTEREST, AccountType.EXPENSE],
]);

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
export function counterLeg(tx: Tx): Tx | undefined {
  const accountType = COUNTER_TYPE.get(tx.type);
  if (accountType === undefined) return undefined;
  if (tx.accountType !== AccountType.USER && tx.accountType !== AccountType.UNSPECIFIED) {
    return undefined;
  }
  const leg = clone(TxSchema, tx);
  leg.quantity = new Big(tx.quantity).times(-1).toString();
  leg.accountType = accountType;
  return leg;
}

/**
 * A money posting derived beside one the source reported, in the same group.
 *
 * The instrument description is the currency code, matching how an ordinary cash
 * row arrives, so nothing downstream has to treat a derived posting specially.
 */
function moneyLeg(from: Tx, type: TxType, accountType: AccountType, quantity: Big): Tx {
  const currency = from.settlementCurrency || from.tradingCurrency;
  return create(TxSchema, {
    ...(from.timestamp ? { timestamp: clone(TimestampSchema, from.timestamp) } : {}),
    instrumentDescription: currency,
    type,
    quantity: quantity.toString(),
    unitPrice: "1",
    account: from.account,
    groupRef: from.groupRef,
    accountType,
    ...(currency
      ? {
          tradingCurrency: currency,
          settlementCurrency: currency,
          identifierHints: [
            create(InstrumentIdentifierSchema, {
              type: IdentifierType.CURRENCY,
              value: currency,
              canonical: false,
            }),
          ],
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
export function feeLeg(from: Tx, fee: Big | undefined): Tx | undefined {
  if (fee === undefined || fee.abs().lt(FEE_EPSILON)) return undefined;
  return moneyLeg(from, TxType.INVEXPENSE, AccountType.USER, fee.abs().times(-1));
}

/**
 * The income a reinvestment consumed.
 *
 * A REINVEST posting increases a holding with no cash row beside it: the income
 * buys the units without ever arriving as money, so there is nothing for the
 * source to have reported and nothing to pair with. Its other side is therefore
 * derived, and unlike counterLeg's it is not a sign flip -- the posting's
 * quantity is a share count, and what balances it is money.
 *
 * The money is the posting's own weight, quantity times unit price, rather than
 * the total the broker printed on the row. The two differ by the rounding in the
 * quoted price, and taking the broker's would leave the group short by a residual
 * of our choosing rather than one the source has.
 */
export function reinvestLeg(from: Tx): Tx | undefined {
  if (from.type !== TxType.REINVEST) return undefined;
  const value = new Big(from.quantity).times(from.unitPrice || "0");
  if (value.abs().lt(FEE_EPSILON)) return undefined;
  return moneyLeg(from, TxType.INCOME, AccountType.INCOME, value.times(-1));
}

/**
 * A group_ref prefix no posting in the batch already starts with, so a
 * synthesised ref cannot collide with a broker's own. Mirrors groupPostings in
 * server/service/ingestion/balance.go.
 */
export function refPrefix(txs: Tx[]): string {
  let prefix = "p";
  for (const tx of txs) {
    while (tx.groupRef.startsWith(prefix)) prefix += "_";
  }
  return prefix;
}

/**
 * The counter-leg of every one-sided cash posting in the batch, for the caller
 * to append. Postings that had no group_ref are given one in place, since a leg
 * only balances the posting it mirrors if the two share a group.
 */
export function counterLegs(txs: Tx[]): Tx[] {
  const prefix = refPrefix(txs);
  const legs: Tx[] = [];
  txs.forEach((tx, i) => {
    // A reinvestment's other side is money it never held, so it is built rather
    // than mirrored; everything else that has one is a sign flip.
    const leg = reinvestLeg(tx) ?? counterLeg(tx);
    if (!leg) return;
    if (!tx.groupRef) {
      tx.groupRef = `${prefix}${i}`;
      leg.groupRef = tx.groupRef;
    }
    legs.push(leg);
  });
  return legs;
}
