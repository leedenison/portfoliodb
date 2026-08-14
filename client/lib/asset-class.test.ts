import { describe, expect, it } from "vitest";
import { AssetClass, AssetClassSchema } from "@/gen/type/v1/type_pb";
import {
  ALL_ASSET_CLASSES,
  ASSET_CLASS_LABELS,
  DEFAULT_ASSET_CLASSES,
  IGNORABLE_ASSET_CLASSES,
  assetClassFromStr,
  assetClassToStr,
} from "./asset-class";

/** Every asset class the proto defines except UNSPECIFIED. */
const declared = AssetClassSchema.values.filter((v) => v.number !== 0);

describe("the asset class lists", () => {
  // The lists are written out by hand, so what matters is that they have not
  // fallen behind the vocabulary they are drawn from. A class added to the proto
  // and left out of ALL_ASSET_CLASSES is one the UI silently cannot show.
  it("covers every class the proto declares, and only those", () => {
    expect([...ALL_ASSET_CLASSES].sort()).toEqual(declared.map((v) => v.number).sort());
  });

  it("has a label for every class, including UNSPECIFIED", () => {
    for (const v of AssetClassSchema.values) {
      expect(ASSET_CLASS_LABELS).toHaveProperty(String(v.number));
    }
    // UNSPECIFIED renders as nothing rather than as the word: it is the absence of
    // a class, and a filter row reading "Unspecified" would invite selecting it.
    expect(ASSET_CLASS_LABELS[AssetClass.ASSET_CLASS_UNSPECIFIED]).toBe("");
    for (const c of ALL_ASSET_CLASSES) {
      expect(ASSET_CLASS_LABELS[c]).not.toBe("");
    }
  });

  it("draws the ignorable and default sets from the full list", () => {
    for (const c of IGNORABLE_ASSET_CLASSES) {
      expect(ALL_ASSET_CLASSES).toContain(c);
    }
    for (const c of DEFAULT_ASSET_CLASSES) {
      expect(ALL_ASSET_CLASSES).toContain(c);
    }
  });

  it("leaves ETF and FX out of the ignorable set", () => {
    // Ignoring a class deletes the transactions in it, so the set is the ones with
    // tx type mappings rather than everything on offer.
    expect(IGNORABLE_ASSET_CLASSES).not.toContain(AssetClass.ETF);
    expect(IGNORABLE_ASSET_CLASSES).not.toContain(AssetClass.FX);
  });
});

describe("assetClassFromStr", () => {
  it("round-trips every class through its stored name", () => {
    // The stored vocabulary is the enum value names, so this is the whole of the
    // conversion in both directions.
    for (const v of declared) {
      expect(assetClassFromStr(v.name)).toBe(v.number);
      expect(assetClassToStr(v.number)).toBe(v.name);
    }
  });

  it.each([
    ["an unknown name", "NOT_A_CLASS"],
    ["an empty string", ""],
    ["the wrong case", "stock"],
    ["a numeric string, which the reverse mapping would answer with a name", "1"],
    ["a property inherited from Object", "toString"],
    ["the prototype", "__proto__"],
  ])("maps %s to UNSPECIFIED", (_name, input) => {
    // The lookup is on a plain object, so anything already on it -- a numeric key
    // from the enum's own reverse mapping, or something off Object.prototype --
    // has to be rejected by the typeof guard rather than returned as a class.
    expect(assetClassFromStr(input)).toBe(AssetClass.ASSET_CLASS_UNSPECIFIED);
  });
});

describe("assetClassToStr", () => {
  it("maps UNSPECIFIED to the empty string", () => {
    // Absent rather than a class named "unspecified", which is what the column
    // holds when a source states nothing.
    expect(assetClassToStr(AssetClass.ASSET_CLASS_UNSPECIFIED)).toBe("");
  });

  it("maps a value outside the vocabulary to the empty string", () => {
    expect(assetClassToStr(9999 as AssetClass)).toBe("");
  });
});
