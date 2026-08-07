import { AssetClass } from "@/gen/type/v1/type_pb";

/** All asset classes except UNSPECIFIED, ordered for UI display. */
export const ALL_ASSET_CLASSES = [
  AssetClass.STOCK,
  AssetClass.ETF,
  AssetClass.OPTION,
  AssetClass.FUTURE,
  AssetClass.FX,
  AssetClass.CASH,
  AssetClass.MUTUAL_FUND,
  AssetClass.FIXED_INCOME,
  AssetClass.UNKNOWN,
] as const;

/** Asset classes that have tx type mappings (selectable for ignoring). */
export const IGNORABLE_ASSET_CLASSES = [
  AssetClass.CASH,
  AssetClass.STOCK,
  AssetClass.OPTION,
  AssetClass.FUTURE,
  AssetClass.FIXED_INCOME,
  AssetClass.MUTUAL_FUND,
  AssetClass.UNKNOWN,
] as const;

/** Default asset classes shown in instrument filters. */
export const DEFAULT_ASSET_CLASSES = new Set([
  AssetClass.STOCK,
  AssetClass.ETF,
  AssetClass.OPTION,
  AssetClass.FUTURE,
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
