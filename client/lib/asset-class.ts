import { AssetClass } from "@/gen/type/v1/type_pb";

// The asset class hierarchy: which AssetClass is under which. The tree is
// written here and in server/assetclass, and both are checked against
// server/assetclass/testdata/tree.json so the two spellings cannot drift.
//
// A leaf is the specificity the system acts on; an internal node is what a less
// specific source says, and both are legal values -- an OFX file reporting
// BUYSTOCK cannot tell a share from an ETF, and EQUITY is how it says so rather
// than picking one.
//
// Only subtree predicates are exposed, no bare equality: may-be includes a
// node's ancestors and must-be does not, and either default is silently wrong,
// one refusing rows a source was honest about and the other reading a coarse
// value as a specific one. Every call site states which question it is asking.
// See docs/adr/0013-security-type-hint-vs-asset-class.md.

/**
 * Maps every value to the node above it. UNKNOWN is the root: what a source
 * says when it does not know whether a row is money or a security. It is never
 * a routing hint -- a posting that made no claim routes as SECURITY.
 */
export const ASSET_CLASS_PARENT: Record<number, AssetClass> = {
  [AssetClass.UNKNOWN]: AssetClass.ASSET_CLASS_UNSPECIFIED,
  [AssetClass.CASH]: AssetClass.UNKNOWN,
  [AssetClass.SECURITY]: AssetClass.UNKNOWN,
  [AssetClass.EQUITY]: AssetClass.SECURITY,
  [AssetClass.STOCK]: AssetClass.EQUITY,
  [AssetClass.ETF]: AssetClass.EQUITY,
  [AssetClass.MUTUAL_FUND]: AssetClass.EQUITY,
  [AssetClass.FIXED_INCOME]: AssetClass.SECURITY,
  [AssetClass.DERIVATIVE]: AssetClass.SECURITY,
  [AssetClass.OPTION]: AssetClass.DERIVATIVE,
  [AssetClass.FUTURE]: AssetClass.DERIVATIVE,
  [AssetClass.FX]: AssetClass.SECURITY,
};

/** Whether c is x or lies in x's subtree. */
function under(c: AssetClass, x: AssetClass): boolean {
  for (let n = c; n !== AssetClass.ASSET_CLASS_UNSPECIFIED; n = ASSET_CLASS_PARENT[n]) {
    if (n === x) return true;
  }
  return false;
}

/**
 * Whether c is one of xs under every reading: c is an x or lies in an x's
 * subtree. The strict question, and the one a corroboration asks -- an internal
 * node answers no, because EQUITY was never a claim about which of the three.
 */
export function mustBe(c: AssetClass, ...xs: AssetClass[]): boolean {
  return xs.some((x) => under(c, x));
}

/**
 * Whether c could be one of xs under some reading: c lies in an x's subtree, or
 * is an ancestor of one. The permissive question, and symmetric, so !mayBe is
 * exactly "these two claims are disjoint".
 */
export function mayBe(c: AssetClass, ...xs: AssetClass[]): boolean {
  return xs.some((x) => under(c, x) || under(x, c));
}

/** All asset classes except UNSPECIFIED, ordered for UI display. */
export const ALL_ASSET_CLASSES = [
  AssetClass.STOCK,
  AssetClass.ETF,
  AssetClass.EQUITY,
  AssetClass.OPTION,
  AssetClass.FUTURE,
  AssetClass.DERIVATIVE,
  AssetClass.FX,
  AssetClass.CASH,
  AssetClass.MUTUAL_FUND,
  AssetClass.FIXED_INCOME,
  AssetClass.SECURITY,
  AssetClass.UNKNOWN,
] as const;

/**
 * Asset classes that have tx type mappings (selectable for ignoring). Leaves
 * only: ignoring a class deletes the transactions in it, and an internal node
 * names no one class to delete.
 */
export const IGNORABLE_ASSET_CLASSES = [
  AssetClass.CASH,
  AssetClass.STOCK,
  AssetClass.OPTION,
  AssetClass.FUTURE,
  AssetClass.FIXED_INCOME,
  AssetClass.MUTUAL_FUND,
  AssetClass.UNKNOWN,
] as const;

/**
 * Default asset classes shown in instrument filters. The coarse nodes above the
 * defaults are in it: an instrument a plugin could classify only as EQUITY is
 * one of these under some reading, and leaving it out would hide it from the
 * view its leaves appear in.
 */
export const DEFAULT_ASSET_CLASSES = new Set([
  AssetClass.STOCK,
  AssetClass.ETF,
  AssetClass.EQUITY,
  AssetClass.OPTION,
  AssetClass.FUTURE,
  AssetClass.DERIVATIVE,
]);

/** Human-readable labels for each asset class. */
export const ASSET_CLASS_LABELS: Record<AssetClass, string> = {
  [AssetClass.ASSET_CLASS_UNSPECIFIED]: "",
  [AssetClass.STOCK]: "Stock",
  [AssetClass.ETF]: "ETF",
  [AssetClass.FIXED_INCOME]: "Fixed Income",
  [AssetClass.MUTUAL_FUND]: "Mutual Fund",
  [AssetClass.OPTION]: "Option",
  [AssetClass.FUTURE]: "Future",
  [AssetClass.CASH]: "Cash",
  [AssetClass.FX]: "FX",
  [AssetClass.EQUITY]: "Share or Fund",
  [AssetClass.DERIVATIVE]: "Derivative",
  [AssetClass.SECURITY]: "Security",
  [AssetClass.UNKNOWN]: "Other",
};

/** Convert a DB/CSV asset class string to the proto enum. */
export function assetClassFromStr(s: string): AssetClass {
  // The enum value names are the stored vocabulary, so the generated enum's own
  // reverse mapping is the whole conversion. Numeric keys resolve to a name
  // rather than a number and are rejected by the typeof guard.
  const v = (AssetClass as unknown as Record<string, unknown>)[s];
  return typeof v === "number" ? (v as AssetClass) : AssetClass.ASSET_CLASS_UNSPECIFIED;
}

/** Convert a proto AssetClass enum to its DB/CSV string. UNSPECIFIED maps to "". */
export function assetClassToStr(ac: AssetClass): string {
  if (ac === AssetClass.ASSET_CLASS_UNSPECIFIED) return "";
  return AssetClass[ac] ?? "";
}
