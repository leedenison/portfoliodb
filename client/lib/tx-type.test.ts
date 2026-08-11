import { readFileSync } from "fs";
import { describe, expect, it } from "vitest";
import { TxType, TxTypeSchema } from "@/gen/type/v1/type_pb";
import { TX_TYPE_PARENT, isAntichain, mayBe, mustBe, resolveTxType } from "./tx-type";

describe("the hierarchy", () => {
  it("covers every enum value exactly once", () => {
    for (const v of TxTypeSchema.values) {
      if (v.number === TxType.TX_TYPE_UNSPECIFIED) {
        expect(TX_TYPE_PARENT[v.number]).toBeUndefined();
        continue;
      }
      expect(TX_TYPE_PARENT[v.number], `${v.name} missing from the parent map`).toBeDefined();
    }
    const known = new Set(TxTypeSchema.values.map((v) => v.number));
    for (const key of Object.keys(TX_TYPE_PARENT)) {
      expect(known.has(Number(key)), `parent map names ${key}, not an enum value`).toBe(true);
    }
  });

  // The golden tree is written by go test ./server/txtype -update, so the Go
  // and TypeScript spellings of the hierarchy cannot drift apart.
  it("matches the Go golden tree", () => {
    const nameOf = new Map(TxTypeSchema.values.map((v) => [v.number, v.name]));
    const tree: Record<string, string> = {};
    for (const [child, parent] of Object.entries(TX_TYPE_PARENT)) {
      const childName = nameOf.get(Number(child));
      if (!childName) continue;
      tree[childName] =
        parent === TxType.TX_TYPE_UNSPECIFIED ? "" : (nameOf.get(parent) ?? "");
    }
    const want = JSON.parse(
      readFileSync("../server/txtype/testdata/tree.json", "utf8"),
    ) as Record<string, string>;
    expect(tree).toEqual(want);
  });
});

describe("mustBe", () => {
  it("holds for a singleton leaf under its branch", () => {
    expect(mustBe([TxType.DIVIDEND], TxType.INCOME)).toBe(true);
  });
  it("requires every candidate to hold", () => {
    expect(mustBe([TxType.TRADE_CASH, TxType.TRANSFER], TxType.TRANSFER)).toBe(false);
  });
  it("does not treat an internal node as its leaf", () => {
    expect(mustBe([TxType.INCOME], TxType.DIVIDEND)).toBe(false);
  });
  it("accepts several targets", () => {
    expect(mustBe([TxType.DIVIDEND, TxType.TRANSACTION_COST], TxType.INCOME, TxType.EXPENSE)).toBe(
      true,
    );
  });
  it("holds for nothing on an empty set", () => {
    expect(mustBe([], TxType.INCOME)).toBe(false);
  });
});

describe("mayBe", () => {
  it("holds when one candidate suffices", () => {
    expect(mayBe([TxType.TRADE_CASH, TxType.TRANSFER], TxType.TRANSFER)).toBe(true);
  });
  it("lets an ancestor be its leaf", () => {
    expect(mayBe([TxType.INCOME], TxType.DIVIDEND)).toBe(true);
  });
  it("rejects a cross-branch candidate", () => {
    expect(mayBe([TxType.TRADE_ASSET], TxType.TRANSFER)).toBe(false);
  });
});

describe("resolveTxType", () => {
  it("resolves a singleton to itself", () => {
    expect(resolveTxType([TxType.TRADE_ASSET])).toBe(TxType.TRADE_ASSET);
  });
  it("resolves siblings to their branch", () => {
    expect(resolveTxType([TxType.DIVIDEND, TxType.INTEREST])).toBe(TxType.INCOME);
  });
  it("resolves a cross-branch set to the root", () => {
    expect(resolveTxType([TxType.TRADE_CASH, TxType.TRANSFER])).toBe(TxType.AMBIGUOUS);
  });
});

describe("isAntichain", () => {
  it("accepts a cross-branch set", () => {
    expect(isAntichain([TxType.TRADE_CASH, TxType.TRANSFER])).toBe(true);
  });
  it("rejects an ancestor beside its descendant", () => {
    expect(isAntichain([TxType.TRANSFER, TxType.TRANSFER_INTERNAL])).toBe(false);
  });
  it("rejects a duplicate", () => {
    expect(isAntichain([TxType.DIVIDEND, TxType.DIVIDEND])).toBe(false);
  });
});
