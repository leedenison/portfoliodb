import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { IdentifierType, IdentifierTypeSchema } from "@/gen/type/v1/type_pb";
import { InstrumentIdentifierSchema } from "@/gen/api/v1/api_pb";
import { currentTicker, VALID_IDENTIFIER_TYPES } from "./identifiers";

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

/**
 * An instrument holds the names it has worn, not just the one it wears. A label
 * has to pick the current one, which is the row with no closing bound.
 */
describe("currentTicker", () => {
  const ident = (type: IdentifierType, value: string, validBefore?: string) =>
    create(InstrumentIdentifierSchema, { type, value, validBefore });

  it("takes the ticker in force over one the instrument has given up", () => {
    const inst = {
      identifiers: [
        ident(IdentifierType.MIC_TICKER, "OLD", "2024-06-10"),
        ident(IdentifierType.MIC_TICKER, "NEW"),
      ],
    };
    expect(currentTicker(inst)).toBe("NEW");
  });

  it("ignores the order the identifiers arrive in", () => {
    const inst = {
      identifiers: [
        ident(IdentifierType.MIC_TICKER, "NEW"),
        ident(IdentifierType.MIC_TICKER, "OLD", "2024-06-10"),
      ],
    };
    expect(currentTicker(inst)).toBe("NEW");
  });

  it("falls back to an OpenFIGI ticker", () => {
    const inst = { identifiers: [ident(IdentifierType.OPENFIGI_TICKER, "FIGI")] };
    expect(currentTicker(inst)).toBe("FIGI");
  });

  it("returns undefined when every ticker has been given up", () => {
    // Better no label than a name the instrument no longer answers to.
    const inst = { identifiers: [ident(IdentifierType.MIC_TICKER, "OLD", "2024-06-10")] };
    expect(currentTicker(inst)).toBeUndefined();
  });

  it("returns undefined for an instrument with no ticker, and for none at all", () => {
    expect(currentTicker({ identifiers: [ident(IdentifierType.ISIN, "US0000000001")] })).toBeUndefined();
    expect(currentTicker({ identifiers: [] })).toBeUndefined();
    expect(currentTicker(undefined)).toBeUndefined();
  });
});
