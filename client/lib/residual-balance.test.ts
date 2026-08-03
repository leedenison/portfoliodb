import { describe, expect, it } from "vitest";
import { AccountType, AssetClass, Broker, TxType } from "@/gen/api/v1/api_pb";
import type { ResidualBalance } from "./portfolio-api";
import { ageInDays, groupByBroker, isMoney, transferAgeBucket } from "./residual-balance";

const NOW = new Date("2026-08-03T12:00:00Z");

function daysAgo(n: number): Date {
  return new Date(NOW.getTime() - n * 24 * 60 * 60 * 1000);
}

function balance(overrides: Partial<ResidualBalance> = {}): ResidualBalance {
  return {
    accountType: AccountType.IMBALANCE,
    broker: Broker.IBKR,
    account: "A1",
    instrumentId: "inst-usd",
    commodity: "USD",
    assetClass: AssetClass.CASH,
    txType: TxType.INCOME,
    balance: -100,
    postingCount: 1,
    ...overrides,
  };
}

describe("transferAgeBucket", () => {
  const cases: { name: string; days: number; want: string }[] = [
    { name: "just imported", days: 0, want: "fresh" },
    { name: "still within the window", days: 6, want: "fresh" },
    { name: "on the stale boundary", days: 7, want: "stale" },
    { name: "late but not missing", days: 29, want: "stale" },
    { name: "on the loud boundary", days: 30, want: "loud" },
    { name: "long gone", days: 400, want: "loud" },
  ];
  for (const c of cases) {
    it(`${c.name} (${c.days}d) is ${c.want}`, () => {
      expect(transferAgeBucket(daysAgo(c.days), NOW)).toBe(c.want);
    });
  }

  it("treats an undated balance as fresh rather than drawing attention to it", () => {
    expect(transferAgeBucket(undefined, NOW)).toBe("fresh");
  });
});

describe("ageInDays", () => {
  it("floors to whole days", () => {
    expect(ageInDays(daysAgo(3.7), NOW)).toBe(3);
  });

  it("is null when the row cannot be dated", () => {
    expect(ageInDays(undefined, NOW)).toBeNull();
  });
});

describe("isMoney", () => {
  it("is true for a currency", () => {
    expect(isMoney(balance())).toBe(true);
  });

  it("is false for a security, whose balance is a share count", () => {
    expect(isMoney(balance({ assetClass: AssetClass.STOCK, commodity: "AAPL" }))).toBe(false);
  });

  it("is false for an unidentified instrument, which we cannot call money", () => {
    expect(isMoney(balance({ assetClass: AssetClass.UNSPECIFIED }))).toBe(false);
  });
});

describe("groupByBroker", () => {
  it("subtotals per commodity and never sums across them", () => {
    const groups = groupByBroker([
      balance({ balance: -100, commodity: "USD" }),
      balance({ balance: -50, commodity: "USD", txType: TxType.BUYSTOCK }),
      balance({ balance: 25, commodity: "AAPL", assetClass: AssetClass.STOCK }),
    ]);

    expect(groups).toHaveLength(1);
    const subtotals = groups[0].subtotals;
    expect(subtotals).toHaveLength(2);
    expect(subtotals.find((s) => s.commodity === "USD")).toMatchObject({
      balance: -150,
      postingCount: 2,
    });
    expect(subtotals.find((s) => s.commodity === "AAPL")).toMatchObject({ balance: 25 });
  });

  it("puts the broker with the largest exposure first", () => {
    const groups = groupByBroker([
      balance({ broker: Broker.IBKR, balance: -10 }),
      balance({ broker: Broker.FIDELITY, balance: -5000 }),
      balance({ broker: Broker.SCHB, balance: -200 }),
    ]);
    expect(groups.map((g) => g.broker)).toEqual([Broker.FIDELITY, Broker.SCHB, Broker.IBKR]);
  });

  it("orders rows within a broker by magnitude, sign aside", () => {
    const groups = groupByBroker([
      balance({ balance: 5, account: "small" }),
      balance({ balance: -900, account: "big" }),
      balance({ balance: 40, account: "middle" }),
    ]);
    expect(groups[0].rows.map((r) => r.account)).toEqual(["big", "middle", "small"]);
  });

  it("returns nothing for no rows", () => {
    expect(groupByBroker([])).toEqual([]);
  });
});
