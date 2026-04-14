import { AssetClass } from "@/gen/api/v1/api_pb";

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
  [AssetClass.UNSPECIFIED]: "",
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

const strToEnum: Record<string, AssetClass> = {
  STOCK: AssetClass.STOCK,
  ETF: AssetClass.ETF,
  FIXED_INCOME: AssetClass.FIXED_INCOME,
  MUTUAL_FUND: AssetClass.MUTUAL_FUND,
  OPTION: AssetClass.OPTION,
  FUTURE: AssetClass.FUTURE,
  CASH: AssetClass.CASH,
  FX: AssetClass.FX,
  UNKNOWN: AssetClass.UNKNOWN,
};

const enumToStr: Record<AssetClass, string> = {
  [AssetClass.UNSPECIFIED]: "",
  [AssetClass.STOCK]: "STOCK",
  [AssetClass.ETF]: "ETF",
  [AssetClass.FIXED_INCOME]: "FIXED_INCOME",
  [AssetClass.MUTUAL_FUND]: "MUTUAL_FUND",
  [AssetClass.OPTION]: "OPTION",
  [AssetClass.FUTURE]: "FUTURE",
  [AssetClass.CASH]: "CASH",
  [AssetClass.FX]: "FX",
  [AssetClass.UNKNOWN]: "UNKNOWN",
};

/** Convert a DB/CSV asset class string to the proto enum. */
export function assetClassFromStr(s: string): AssetClass {
  return strToEnum[s] ?? AssetClass.UNSPECIFIED;
}

/** Convert a proto AssetClass enum to its DB/CSV string. */
export function assetClassToStr(ac: AssetClass): string {
  return enumToStr[ac] ?? "";
}
