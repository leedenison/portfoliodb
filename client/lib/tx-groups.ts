import type { PortfolioTx } from "@/gen/api/v1/api_pb";
import { AccountType, TxType } from "@/gen/type/v1/type_pb";
import { Big } from "@/lib/decimal";

/**
 * A page of postings, assembled back into the events they are legs of.
 *
 * The server pages by group and returns every posting of the groups a page
 * covers (see the group_id field on Tx), so a group here is whole and nothing
 * has to be fetched to complete it. What is left is deciding which leg speaks
 * for the event, which is what the list shows before it is expanded.
 */
export interface TxGroup {
  /** The group id every posting in it carries. */
  id: string;
  /** The leg the event is shown as. */
  principal: PortfolioTx;
  /**
   * The remaining legs worth showing: what the source stated first, then what the
   * server routed to balance the group, each in the order the server returned
   * them. Empty when the event has nothing to add, in which case it does not
   * expand.
   */
  rest: PortfolioTx[];
}

/** The purpose a leg the server routed to balance a group carries; db.RoutedPurpose. */
const ROUTED_PURPOSE = "RESIDUAL";

/** Whether the server routed this leg to make its group sum to zero. */
function routed(p: PortfolioTx): boolean {
  return p.tx?.syntheticPurpose === ROUTED_PURPOSE;
}

/**
 * A leg the server derived from the principal alone, which restates it rather
 * than adding to it: the income leg of a dividend, the expense leg of a charge,
 * the equity half of an opening balance. Each is equal and opposite to the leg
 * the row already shows, so at group level it says nothing.
 *
 * The synthetic purpose is what separates these from the legs worth showing, and
 * account type cannot: a routed residual lands in a non-USER account type too.
 * A leg with the RESIDUAL purpose is what the group failed to balance to, which
 * the source did not supply and the row cannot imply -- an imbalance is missing
 * data, a clearing leg is value in flight awaiting its other side, and a rounding
 * residual is the source's own figures disagreeing. Those stay.
 *
 * The expansion therefore does not visibly sum to zero. Verifying that is a
 * question about the ledger rather than about one row, and nothing is dropped
 * from anywhere but this view.
 */
function derivedFromPrincipal(p: PortfolioTx): boolean {
  const purpose = p.tx?.syntheticPurpose;
  return !!purpose && purpose !== ROUTED_PURPOSE;
}

/**
 * How strong a claim a resolved type has to standing for the whole event,
 * strongest first. A trade is its asset leg, income is the income itself, a
 * transfer is the thing transferred, and a charge is the charge; cash comes last
 * because a trade's cash leg is the consideration for a leg that says more.
 *
 * An internal node ranks with its branch rather than above it: a source that
 * only managed INCOME still describes the event as income. AMBIGUOUS and unset
 * rank below everything, so a group with one resolved leg shows that leg.
 */
const PRINCIPAL_RANK: Record<number, number> = {
  [TxType.TRADE_ASSET]: 1,
  [TxType.TRADE]: 2,
  [TxType.INCOME]: 3,
  [TxType.DIVIDEND]: 3,
  [TxType.INTEREST]: 3,
  [TxType.RETURN_OF_CAPITAL]: 3,
  [TxType.TRANSFER]: 4,
  [TxType.TRANSFER_INTERNAL]: 4,
  [TxType.TRANSFER_EXTERNAL]: 4,
  [TxType.EXPENSE]: 5,
  [TxType.TRANSACTION_COST]: 5,
  [TxType.HOLDING_COST]: 5,
  [TxType.FINANCING_COST]: 5,
  [TxType.TRADE_CASH]: 6,
  [TxType.AMBIGUOUS]: 7,
  [TxType.TX_TYPE_UNSPECIFIED]: 8,
};

const LOWEST_RANK = 9;

function rank(p: PortfolioTx): number {
  const t = p.tx?.resolvedTxType;
  return (t !== undefined ? PRINCIPAL_RANK[t] : undefined) ?? LOWEST_RANK;
}

/**
 * Whether a is the better leg to show the event as: the stronger type, and
 * between two of the same type the larger amount, which is what separates a
 * trade's commission from the trade. The magnitudes are compared exactly rather
 * than as floats -- they are decimal and two legs of one event are routinely
 * close -- though nothing is computed from them.
 */
function better(a: PortfolioTx, b: PortfolioTx): boolean {
  const ra = rank(a);
  const rb = rank(b);
  if (ra !== rb) return ra < rb;
  return Big(a.tx?.quantity ?? "0")
    .abs()
    .gt(Big(b.tx?.quantity ?? "0").abs());
}

/**
 * The leg an event is shown as. Only the user's own postings are candidates:
 * the counter legs a group balances against -- the income a dividend came from,
 * the expense account a fee landed in, an unmatched transfer's clearing leg --
 * restate the same amount in an account the user does not hold, and showing one
 * of those as the event would name an account nobody recognises. A group with no
 * user posting at all falls back to all of them, so nothing is ever unshowable.
 *
 * An unset account type counts as a user posting, which is what it means.
 */
function principalOf(postings: PortfolioTx[]): PortfolioTx {
  const user = postings.filter(
    (p) =>
      p.tx?.accountType === AccountType.USER ||
      p.tx?.accountType === AccountType.UNSPECIFIED
  );
  const candidates = user.length > 0 ? user : postings;
  return candidates.reduce((best, p) => (better(p, best) ? p : best));
}

/**
 * Assembles a page of postings into its events, in the order the page supplied
 * them. Postings of one group arrive contiguously, but they are keyed rather
 * than run-length grouped so that the order the rows are shown in and the order
 * they are grouped by stay independent.
 *
 * A group's principal is chosen from all of its legs and its rest is filtered
 * afterwards, so what is hidden cannot change which leg speaks for the event.
 */
export function groupTxs(txs: readonly PortfolioTx[]): TxGroup[] {
  const byGroup = new Map<string, PortfolioTx[]>();
  for (const t of txs) {
    // A posting with no group id cannot have come from the server, which always
    // sets one. Keying it on its own index would be a lie either way, so they
    // share a bucket and the page still renders.
    const id = t.tx?.groupId ?? "";
    const postings = byGroup.get(id);
    if (postings) postings.push(t);
    else byGroup.set(id, [t]);
  }
  return Array.from(byGroup, ([id, postings]) => {
    const principal = principalOf(postings);
    // Routed legs last. What a source stated is the event; what the server routed
    // is what the event failed to account for, and reading the stated legs first
    // is reading the source before the correction. The sort is stable, so the
    // page's order survives within each.
    const rest = postings
      .filter((p) => p !== principal && p.tx && !derivedFromPrincipal(p))
      .sort((a, b) => Number(routed(a)) - Number(routed(b)));
    return { id, principal, rest };
  });
}
