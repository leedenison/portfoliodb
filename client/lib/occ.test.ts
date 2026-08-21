import { describe, it, expect } from "vitest";
import { Big } from "@/lib/decimal";
import { buildOcc, occCompact, occPadded, occParts } from "./occ";

/** The expiry OCC encodes as 260320, built the way parseOfxDate builds one. */
function expiry(y: number, m: number, d: number): Date {
  return new Date(y, m - 1, d);
}

describe("occCompact", () => {
  it("normalises a padded symbol to its compact form", () => {
    expect(occCompact("AMD   260320P00085000")).toBe("AMD260320P00085000");
  });

  it("accepts a symbol that is already compact", () => {
    expect(occCompact("AMD260320P00085000")).toBe("AMD260320P00085000");
  });

  it("uppercases and trims", () => {
    expect(occCompact("  amd   260320p00085000 ")).toBe("AMD260320P00085000");
  });

  it("rejects IBKR's rendering of a Eurex contract", () => {
    // The case issue 0145 is about: a real contract, printed in a symbology OCC
    // has nothing to do with. Stripping its spaces leaves 20250919560M, which is
    // not an expiry, a right and a strike.
    expect(occCompact("P RHM  20250919 560 M")).toBeUndefined();
  });

  it("rejects a bare ticker", () => {
    expect(occCompact("AMD")).toBeUndefined();
  });

  it("rejects a root longer than six characters", () => {
    expect(occCompact("TOOLONG260320P00085000")).toBeUndefined();
  });

  it("rejects a symbol with no root", () => {
    expect(occCompact("260320P00085000")).toBeUndefined();
  });

  it("rejects a right that is neither call nor put", () => {
    expect(occCompact("AMD260320X00085000")).toBeUndefined();
  });
});

describe("occParts", () => {
  it("takes a padded symbol apart", () => {
    expect(occParts("AMD   260320P00085000")).toEqual({
      compact: "AMD260320P00085000",
      root: "AMD",
    });
  });

  it("takes a six-character root apart", () => {
    expect(occParts("GOOGL 260320P00155000")?.root).toBe("GOOGL");
  });

  it("returns nothing for a value that is not a symbol", () => {
    expect(occParts("P RHM  20250919 560 M")).toBeUndefined();
  });
});

describe("occPadded", () => {
  it("pads a compact symbol to twenty-one characters", () => {
    const padded = occPadded("AMD260320P00085000");
    expect(padded).toBe("AMD   260320P00085000");
    expect(padded).toHaveLength(21);
  });

  it("leaves an already-padded symbol alone", () => {
    expect(occPadded("AMD   260320P00085000")).toBe("AMD   260320P00085000");
  });

  it("returns nothing for a value that is not a symbol", () => {
    expect(occPadded("P RHM  20250919 560 M")).toBeUndefined();
  });
});

describe("buildOcc", () => {
  it("builds the symbol the terms name", () => {
    expect(buildOcc("AMD", expiry(2026, 3, 20), "P", new Big(85))).toBe(
      "AMD260320P00085000",
    );
  });

  it("encodes a strike with places in it", () => {
    expect(buildOcc("ARM", expiry(2026, 6, 18), "P", new Big("82.5"))).toBe(
      "ARM260618P00082500",
    );
  });

  it("encodes a strike to the last place the format carries", () => {
    expect(buildOcc("XYZ", expiry(2026, 1, 16), "C", new Big("12.345"))).toBe(
      "XYZ260116C00012345",
    );
  });

  it("round-trips through the parser it mirrors", () => {
    const built = buildOcc("BRKB", expiry(2026, 9, 18), "P", new Big(470));
    expect(occPadded(built!)).toBe("BRKB  260918P00470000");
  });

  it("refuses a strike finer than the format encodes", () => {
    // Rounding it would name a contract nobody traded, so it names none.
    expect(buildOcc("XYZ", expiry(2026, 1, 16), "C", new Big("12.3456"))).toBeUndefined();
  });

  it("refuses a strike the eight digits cannot hold", () => {
    expect(buildOcc("XYZ", expiry(2026, 1, 16), "C", new Big(100000))).toBeUndefined();
  });

  it("refuses a negative strike", () => {
    expect(buildOcc("XYZ", expiry(2026, 1, 16), "C", new Big(-1))).toBeUndefined();
  });

  it("refuses a root longer than six characters", () => {
    expect(buildOcc("TOOLONG", expiry(2026, 3, 20), "P", new Big(85))).toBeUndefined();
  });

  it("refuses an empty root", () => {
    expect(buildOcc("", expiry(2026, 3, 20), "P", new Big(85))).toBeUndefined();
  });

  it("refuses a root that is not a symbol", () => {
    // What IBKR prints for a Eurex contract, offered as a root: it is the reason
    // the ticker is checked rather than taken apart and rebuilt.
    expect(buildOcc("P RHM", expiry(2025, 9, 19), "P", new Big(560))).toBeUndefined();
  });

  it("refuses an unparseable expiry", () => {
    expect(buildOcc("XYZ", new Date(NaN), "C", new Big(85))).toBeUndefined();
  });
});
