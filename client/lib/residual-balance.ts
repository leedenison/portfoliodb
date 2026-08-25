import { AssetClass } from "@/gen/type/v1/type_pb";
import { mustBe } from "./asset-class";
import type { ResidualBalance } from "./portfolio-api";
import { Big } from "@/lib/decimal";

/**
 * How old a transfer balance must be before it is worth a second look. This is
 * age alone: nothing pairs the two sides of a journal until transfers are
 * matched, so an old balance may be a settled transfer rather than a missing
 * side.
 */
export const TRANSFER_STALE_DAYS = 7;

export const TRANSFER_LOUD_DAYS = 30;

export type AgeBucket = "fresh" | "stale" | "loud";

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * Bucket a transfer balance by the age of its oldest posting. An undated one is
 * treated as fresh: the report should not draw attention to something it cannot
 * date.
 */
export function transferAgeBucket(oldest: Date | undefined, now: Date): AgeBucket {
  if (!oldest) return "fresh";
  const days = (now.getTime() - oldest.getTime()) / DAY_MS;
  if (days >= TRANSFER_LOUD_DAYS) return "loud";
  if (days >= TRANSFER_STALE_DAYS) return "stale";
  return "fresh";
}

/** Whole days since the oldest contributing posting. */
export function ageInDays(oldest: Date | undefined, now: Date): number | null {
  if (!oldest) return null;
  return Math.floor((now.getTime() - oldest.getTime()) / DAY_MS);
}

/**
 * A balance in a currency is money; one in a security is a quantity of shares.
 * The two are never summed and never formatted the same way. An unidentified
 * instrument counts as a quantity: rendering an unknown commodity as money would
 * assert something the data does not say.
 *
 * The strict question, for that reason. A class that merely could be cash --
 * the root of the vocabulary, which is every instrument there is -- has not
 * said this balance is money.
 */
export function isMoney(b: { assetClass: AssetClass }): boolean {
  return mustBe(b.assetClass, AssetClass.CASH);
}

export interface CommoditySubtotal {
  commodity: string;
  assetClass: AssetClass;
  /** Decimal string, summed exactly from the rows it covers. */
  balance: string;
  postingCount: number;
}

export interface BrokerGroup {
  broker: number;
  /** Per-commodity totals: the headline number for this broker. */
  subtotals: CommoditySubtotal[];
  rows: ResidualBalance[];
}

/**
 * Group rows by broker, with a per-commodity subtotal each. Commodities are kept
 * apart rather than summed because a residual in USD and one in AAPL shares are
 * not addable, and a single "total" over them would be a fiction.
 *
 * Brokers are ordered by their largest single commodity exposure, and rows within
 * a broker by magnitude, so the converter worth fixing first is at the top.
 */
export function groupByBroker(rows: ResidualBalance[]): BrokerGroup[] {
  const byBroker = new Map<number, ResidualBalance[]>();
  for (const r of rows) {
    const existing = byBroker.get(r.broker);
    if (existing) existing.push(r);
    else byBroker.set(r.broker, [r]);
  }

  const groups: BrokerGroup[] = [];
  for (const [broker, brokerRows] of byBroker) {
    const byCommodity = new Map<string, CommoditySubtotal>();
    for (const r of brokerRows) {
      const existing = byCommodity.get(r.commodity);
      if (existing) {
        // A sum of exact values, so it is summed exactly. This is the imbalance
        // report: the one screen whose whole purpose is to find discrepancies to
        // the penny, so re-floating the figures here would defeat it.
        existing.balance = new Big(existing.balance).plus(r.balance).toString();
        existing.postingCount += r.postingCount;
      } else {
        byCommodity.set(r.commodity, {
          commodity: r.commodity,
          assetClass: r.assetClass,
          balance: r.balance,
          postingCount: r.postingCount,
        });
      }
    }
    groups.push({
      broker,
      subtotals: [...byCommodity.values()].sort(byMagnitude),
      rows: [...brokerRows].sort(byMagnitude),
    });
  }

  return groups.sort((a, b) => maxMagnitude(b).cmp(maxMagnitude(a)));
}

/** Descending by absolute balance. Lexicographic order would be wrong here. */
function byMagnitude(a: { balance: string }, b: { balance: string }): number {
  return new Big(b.balance).abs().cmp(new Big(a.balance).abs());
}

function maxMagnitude(g: BrokerGroup): Big {
  return g.subtotals.reduce(
    (max, s) => {
      const v = new Big(s.balance).abs();
      return v.gt(max) ? v : max;
    },
    new Big(0),
  );
}
