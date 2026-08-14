import { describe, expect, it } from "vitest";
import { IdentifierTypeSchema } from "@/gen/type/v1/type_pb";
import { VALID_IDENTIFIER_TYPES } from "./identifiers";

/**
 * The set an import parser checks a hint against. It is derived from the proto
 * rather than written out, so what is worth asserting is that the derivation says
 * what it means to: every name the vocabulary defines, and nothing that is not one.
 */
describe("VALID_IDENTIFIER_TYPES", () => {
  it("holds every identifier type the proto declares", () => {
    const declared = IdentifierTypeSchema.values.filter((v) => v.number !== 0).map((v) => v.name);
    expect([...VALID_IDENTIFIER_TYPES].sort()).toEqual(declared.sort());
    expect(VALID_IDENTIFIER_TYPES.size).toBe(declared.length);
  });

  it("holds the ones the parsers actually emit", () => {
    // Named rather than counted, so that a rename in the proto fails here with the
    // old name in hand instead of only moving a total.
    for (const t of ["ISIN", "CUSIP", "OCC", "CURRENCY", "MIC_TICKER", "BROKER_DESCRIPTION"]) {
      expect(VALID_IDENTIFIER_TYPES.has(t)).toBe(true);
    }
  });

  it("excludes the unspecified value", () => {
    // A hint of no type is the absence of a hint. Admitting it would let a row
    // carrying nothing pass a check meant to establish that it carried something.
    const unspecified = IdentifierTypeSchema.values.find((v) => v.number === 0)!;
    expect(VALID_IDENTIFIER_TYPES.has(unspecified.name)).toBe(false);
  });

  it.each(["", "ticker", "NOT_A_TYPE", "0"])("does not hold %j", (s) => {
    expect(VALID_IDENTIFIER_TYPES.has(s)).toBe(false);
  });
});
