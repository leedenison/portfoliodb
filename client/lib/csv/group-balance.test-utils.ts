/**
 * Checks converter output the way the server will: every group's postings
 * weigh to zero once the server has posted the sides it derives.
 *
 * A group's postings are in different commodities, so a plain sum says nothing
 * -- a buy is +10 AAPL and -1855 USD. This mirrors weightOf in
 * server/service/ingestion/balance.go, reduced to the cases a converter can
 * produce: a security leg converts to money at its price times its contract
 * size, and everything else weighs its own quantity in the settlement currency.
 * It does not model a securities journal, whose commodity is neither.
 *
 * It also mirrors residual.Boundary: a posting whose declared type must be income
 * or expense has its other side posted by the server, so its weight is cancelled
 * here rather than being left as something the converter failed to balance. What
 * remains is what the converter is answerable for.
 *
 * Not a test file itself -- the name keeps it out of vitest's include glob.
 */

import { expect } from "vitest";
import type { Posting } from "@/gen/archive/v1/txs_pb";
import { Big } from "@/lib/decimal";
import { AccountType, IdentifierType, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";

/**
 * Half a cent, matching moneyTolerance in balance.go.
 *
 * The weights below are exact now, so a group written to full precision balances
 * to exactly zero. The tolerance stays because it is not a floating-point fudge:
 * a source written to 2dp that balances to within half a cent is balanced. The
 * server does post the difference, to SOURCE_ROUNDING rather than to IMBALANCE,
 * but that is the server classifying its own residual and says nothing about
 * whether the converter did its job -- which is what this file checks.
 */
const TOLERANCE = new Big("0.005");

/**
 * The OCC standard deliverable, matching optionContractSize in balance.go.
 *
 * balance.go reads the size off the resolved instrument's asset class; a
 * converter has resolved nothing yet, so an OCC hint stands in for one. The
 * symbology exists only for a standardised contract, so the hint and the asset
 * class agree. A contract_multiplier left behind by a corporate action does not,
 * but a converter cannot know one either.
 */
const OPTION_CONTRACT_SIZE = new Big(100);

/** What one unit of quantity delivers: 100 for an option contract, else 1. */
function contractSize(tx: Posting): Big {
  const isOption = tx.identifierHints.some((h) => h.type === IdentifierType.OCC);
  return isOption ? OPTION_CONTRACT_SIZE : new Big(1);
}

/** What a posting contributes to its group's balance, and in what commodity. */
export function weigh(tx: Posting): { amount: Big; commodity: string } {
  const settle = (tx.settlementCurrency || tx.tradingCurrency || "").toUpperCase();
  const qty = new Big(tx.quantity);
  // The asset leg of a trade is the one type whose counter-leg is money, and
  // the every-candidate rule means an ambiguous set does not convert.
  if (mustBe(tx.brokerTxType, TxType.TRADE_ASSET)) {
    // With no price there is nothing to convert at, so the residual stays in the
    // security -- which is the signal that the source omitted a price.
    if (tx.unitPrice === undefined || !settle) {
      return { amount: qty, commodity: tx.instrumentDescription };
    }
    return { amount: qty.times(tx.unitPrice).times(contractSize(tx)), commodity: settle };
  }
  return { amount: qty, commodity: settle || tx.instrumentDescription };
}

/**
 * Whether the server will post the other side of this posting itself, mirroring
 * residual.Boundary and the account-type guard beside it in balance.go.
 */
function serverBalances(tx: Posting): boolean {
  if (tx.accountType !== AccountType.USER && tx.accountType !== AccountType.UNSPECIFIED) {
    return false;
  }
  return mustBe(tx.brokerTxType, TxType.INCOME) || mustBe(tx.brokerTxType, TxType.EXPENSE);
}

/**
 * What the batch fails to account for, per commodity, with the commodities that
 * balance left out. Empty when everything does.
 *
 * Over the whole batch rather than per group, because a converter no longer says
 * what the groups are: the server partitions the postings once they are stored. A
 * converter's job is that the postings it emits account for each other, and that is
 * a property of the batch.
 */
export function residuals(txs: Posting[]): Record<string, string> {
  const sums: Record<string, Big> = {};
  for (const tx of txs) {
    const { amount, commodity } = weigh(tx);
    // A posting the server posts the other side of contributes nothing left over:
    // its weight and the boundary leg's cancel.
    const net = serverBalances(tx) ? new Big(0) : amount;
    sums[commodity] = (sums[commodity] ?? new Big(0)).plus(net);
  }
  const out: Record<string, string> = {};
  for (const [commodity, v] of Object.entries(sums)) {
    if (v.abs().gte(TOLERANCE)) out[commodity] = v.toString();
  }
  return out;
}

/** Asserts the batch accounts for itself in every commodity. */
export function expectGroupsBalance(txs: Posting[]): void {
  expect(residuals(txs)).toEqual({});
}
