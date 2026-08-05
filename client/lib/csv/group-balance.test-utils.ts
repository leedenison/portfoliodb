/**
 * Checks converter output the way the server will: every group's postings
 * weigh to zero.
 *
 * A group's postings are in different commodities, so a plain sum says nothing
 * -- a buy is +10 AAPL and -1855 USD. This mirrors weightOf in
 * server/service/ingestion/balance.go, reduced to the cases a converter can
 * produce: a security leg converts to money at its price, and everything else
 * weighs its own quantity in the settlement currency. It does not model a
 * securities journal, whose commodity is neither.
 *
 * Not a test file itself -- the name keeps it out of vitest's include glob.
 */

import { expect } from "vitest";
import type { Tx } from "@/gen/api/v1/api_pb";
import { Big } from "@/lib/decimal";
import { TxType } from "@/gen/api/v1/api_pb";

/** Types whose counter-leg is money, so the posting converts at its price. */
const EXCHANGE_TYPES = new Set<TxType>([
  TxType.BUYDEBT,
  TxType.BUYFUTURE,
  TxType.BUYMF,
  TxType.BUYOPT,
  TxType.BUYOTHER,
  TxType.BUYSTOCK,
  TxType.SELLDEBT,
  TxType.SELLFUTURE,
  TxType.SELLMF,
  TxType.SELLOPT,
  TxType.SELLOTHER,
  TxType.SELLSTOCK,
  TxType.REINVEST,
  TxType.CLOSUREOPT,
]);

/**
 * Half a cent, matching moneyTolerance in balance.go.
 *
 * The weights below are exact now, so a group written to full precision balances
 * to exactly zero. The tolerance stays because it is not a floating-point fudge:
 * a source written to 2dp that balances to within half a cent is balanced, and
 * the server routes nothing below this either.
 */
const TOLERANCE = new Big("0.005");

/** What a posting contributes to its group's balance, and in what commodity. */
export function weigh(tx: Tx): { amount: Big; commodity: string } {
  const settle = (tx.settlementCurrency || tx.tradingCurrency).toUpperCase();
  const qty = new Big(tx.quantity);
  if (EXCHANGE_TYPES.has(tx.type)) {
    // With no price there is nothing to convert at, so the residual stays in the
    // security -- which is the signal that the source omitted a price.
    if (tx.unitPrice === undefined || !settle) {
      return { amount: qty, commodity: tx.instrumentDescription };
    }
    return { amount: qty.times(tx.unitPrice), commodity: settle };
  }
  return { amount: qty, commodity: settle || tx.instrumentDescription };
}

/**
 * What each group fails to account for, keyed by group_ref then commodity, with
 * balanced groups and commodities left out. Empty when everything balances.
 *
 * A posting with no group_ref is its own group, as the server treats it.
 */
export function residuals(txs: Tx[]): Record<string, Record<string, string>> {
  const sums: Record<string, Record<string, Big>> = {};
  txs.forEach((tx, i) => {
    const ref = tx.groupRef || `#${i}`;
    const { amount, commodity } = weigh(tx);
    sums[ref] ??= {};
    sums[ref][commodity] = (sums[ref][commodity] ?? new Big(0)).plus(amount);
  });
  const out: Record<string, Record<string, string>> = {};
  for (const [ref, byCommodity] of Object.entries(sums)) {
    const left = Object.entries(byCommodity)
      .filter(([, v]) => v.abs().gte(TOLERANCE))
      .map(([c, v]) => [c, v.toString()] as const);
    if (left.length > 0) out[ref] = Object.fromEntries(left);
  }
  return out;
}

/** Asserts every group in the batch balances. */
export function expectGroupsBalance(txs: Tx[]): void {
  expect(residuals(txs)).toEqual({});
}
