import { AccountType, TxType } from "@/gen/type/v1/type_pb";

// The transaction type hierarchy: which TxType is under which, what a declared
// set of candidates means, and how a set resolves to the single value
// everything downstream reads. The tree is written here and in server/txtype,
// and both are checked against server/txtype/testdata/tree.json so the two
// spellings cannot drift.
//
// Only subtree predicates are exposed, no bare equality: may-be includes a
// node's ancestors and must-be does not, and either default is silently wrong,
// one dropping rows and the other double-counting. Every call site states
// which question it is asking.
// See docs/adr/0044-tx-type-is-declared-and-resolved.md.

/**
 * Maps every value to the node above it. AMBIGUOUS is the root: it is the
 * resolved spelling of "could be any branch", never legal in a declared set.
 */
export const TX_TYPE_PARENT: Record<number, TxType> = {
  [TxType.TRADE]: TxType.AMBIGUOUS,
  [TxType.TRADE_ASSET]: TxType.TRADE,
  [TxType.TRADE_CASH]: TxType.TRADE,
  [TxType.INCOME]: TxType.AMBIGUOUS,
  [TxType.DIVIDEND]: TxType.INCOME,
  [TxType.INTEREST]: TxType.INCOME,
  [TxType.RETURN_OF_CAPITAL]: TxType.INCOME,
  [TxType.EXPENSE]: TxType.AMBIGUOUS,
  [TxType.TRANSACTION_COST]: TxType.EXPENSE,
  [TxType.HOLDING_COST]: TxType.EXPENSE,
  [TxType.FINANCING_COST]: TxType.EXPENSE,
  [TxType.TRANSFER]: TxType.AMBIGUOUS,
  [TxType.TRANSFER_INTERNAL]: TxType.TRANSFER,
  [TxType.TRANSFER_EXTERNAL]: TxType.TRANSFER,
  [TxType.AMBIGUOUS]: TxType.TX_TYPE_UNSPECIFIED,
};

/** Whether t is x or lies in x's subtree. */
function under(t: TxType, x: TxType): boolean {
  for (let n = t; n !== TxType.TX_TYPE_UNSPECIFIED; n = TX_TYPE_PARENT[n]) {
    if (n === x) return true;
  }
  return false;
}

/**
 * Whether the posting is one of xs under every reading: every member of the
 * declared set lies in some x's subtree. This is the predicate a rule fires
 * on, so an ambiguous row is not treated as any one of its candidates. False
 * for an empty set.
 */
export function mustBe(set: readonly TxType[], ...xs: TxType[]): boolean {
  if (set.length === 0) return false;
  return set.every((t) => xs.some((x) => under(t, x)));
}

/**
 * Whether the posting could be one of xs under some reading: some member of
 * the declared set lies in an x's subtree, or is an ancestor of an x -- a
 * source that said INCOME may yet turn out to have meant DIVIDEND.
 */
export function mayBe(set: readonly TxType[], ...xs: TxType[]): boolean {
  return set.some((t) => xs.some((x) => under(t, x) || under(x, t)));
}

/**
 * The nearest common ancestor of the set: the member itself for a singleton,
 * the shared branch node for siblings, AMBIGUOUS for a cross-branch set,
 * TX_TYPE_UNSPECIFIED for an empty one.
 */
export function resolveTxType(set: readonly TxType[]): TxType {
  if (set.length === 0) return TxType.TX_TYPE_UNSPECIFIED;
  let anc = set[0];
  for (const t of set.slice(1)) {
    while (!under(t, anc)) anc = TX_TYPE_PARENT[anc];
  }
  return anc;
}

/**
 * Whether no member of the set is an ancestor of another, itself included.
 * {TRANSFER, TRANSFER_INTERNAL} is meaningless -- the ancestor already covers
 * the descendant.
 */
export function isAntichain(set: readonly TxType[]): boolean {
  return set.every((a, i) => set.every((b, j) => i === j || !under(b, a)));
}

/** Human-readable labels for each tx type. */
export const TX_TYPE_LABEL: Record<number, string> = {
  [TxType.TRADE]: "Trade",
  [TxType.TRADE_ASSET]: "Trade (Asset)",
  [TxType.TRADE_CASH]: "Trade (Cash)",
  [TxType.INCOME]: "Income",
  [TxType.DIVIDEND]: "Dividend",
  [TxType.INTEREST]: "Interest",
  [TxType.RETURN_OF_CAPITAL]: "Return of Capital",
  [TxType.EXPENSE]: "Expense",
  [TxType.TRANSACTION_COST]: "Transaction Cost",
  [TxType.HOLDING_COST]: "Holding Cost",
  [TxType.FINANCING_COST]: "Financing Cost",
  [TxType.TRANSFER]: "Transfer",
  [TxType.TRANSFER_INTERNAL]: "Transfer (Internal)",
  [TxType.TRANSFER_EXTERNAL]: "Transfer (External)",
  [TxType.AMBIGUOUS]: "Ambiguous",
};

// Labels for the non-asset side of an event. USER postings are the ordinary case and
// carry no badge; the rest are shown so that a group's legs can be told apart, which
// is also how an imbalance becomes visible rather than silently absorbed.
export const ACCOUNT_TYPE_LABEL: Record<number, string> = {
  [AccountType.EQUITY]: "Equity",
  [AccountType.INCOME]: "Income",
  [AccountType.EXPENSE]: "Expense",
  [AccountType.IMBALANCE]: "Imbalance",
  [AccountType.TRANSFER_CLEARING]: "In Flight",
  [AccountType.SOURCE_ROUNDING]: "Rounding",
};
