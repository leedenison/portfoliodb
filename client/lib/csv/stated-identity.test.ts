/**
 * The within-file identity check, and the fact that every converter gets it.
 *
 * The second half matters as much as the first: the check is wrapped around
 * whatever is registered rather than called by each converter, so a converter
 * added later cannot forget it. A test that only exercised the function directly
 * would keep passing if the wrapper were removed.
 */

import { create } from "@bufbuild/protobuf";
import { beforeAll, describe, expect, it } from "vitest";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import type { Posting } from "@/gen/archive/v1/txs_pb";
import { Broker, IdentifierType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult } from "@/lib/csv/parse-result";
import { statedIdentityErrors } from "@/lib/csv/stated-identity";
import { getBrokerEntry, register } from "@/lib/csv/converters/registry";

interface Hint {
  type: IdentifierType;
  value: string;
  domain?: string;
}

function posting(instrumentDescription: string, ...hints: Hint[]): Posting {
  return create(PostingSchema, {
    instrumentDescription,
    identifierHints: hints.map((h) =>
      create(InstrumentRefSchema, { type: h.type, value: h.value, domain: h.domain ?? "" }),
    ),
  });
}

describe("statedIdentityErrors", () => {
  it("refuses one description that is two securities", () => {
    const errors = statedIdentityErrors([
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "GB0000000001" }),
    ]);

    expect(errors).toHaveLength(1);
    expect(errors[0]!.rowIndex).toBe(1);
    expect(errors[0]!.field).toBe("identifier_hints");
    // Both values named, and the row the other came from: the reader has to see
    // what disagreed with what, since nothing says which is right.
    expect(errors[0]!.message).toContain("US0378331005");
    expect(errors[0]!.message).toContain("GB0000000001");
    expect(errors[0]!.message).toContain("row 0");
  });

  it("finds a row that contradicts itself without needing a second row", () => {
    const errors = statedIdentityErrors([
      posting(
        "APPLE INC COM",
        { type: IdentifierType.ISIN, value: "US0378331005" },
        { type: IdentifierType.ISIN, value: "GB0000000001" },
      ),
    ]);
    expect(errors).toHaveLength(1);
  });

  it("reports each disagreement against the first value stated", () => {
    const errors = statedIdentityErrors([
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "GB0000000001" }),
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "FR0000000002" }),
    ]);
    expect(errors).toHaveLength(2);
    expect(errors.map((e) => e.rowIndex)).toEqual([1, 2]);
  });

  // Each of these is a file saying something ordinary. Reading any of them as a
  // contradiction would refuse uploads that are fine, which is the failure that
  // costs more than the one this check exists for.
  describe("leaves legitimate files alone", () => {
    it("one security quoted in two places", () => {
      // A ticker under two domains names two listings, not two securities.
      expect(
        statedIdentityErrors([
          posting("VANGUARD S&P 500", {
            type: IdentifierType.MIC_TICKER,
            value: "VOO",
            domain: "XNAS",
          }),
          posting("VANGUARD S&P 500", {
            type: IdentifierType.MIC_TICKER,
            value: "VUSA",
            domain: "XLON",
          }),
        ]),
      ).toEqual([]);
    });

    it("two descriptions for one security", () => {
      // A broker writes a security several ways and they resolve to one
      // instrument, which is the point of storing the mapping.
      expect(
        statedIdentityErrors([
          posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
          posting("APPLE INC", { type: IdentifierType.ISIN, value: "US0378331005" }),
        ]),
      ).toEqual([]);
    });

    it("different subjects saying different things", () => {
      expect(
        statedIdentityErrors([
          posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
          posting("APPLE INC COM", { type: IdentifierType.CUSIP, value: "037833100" }),
        ]),
      ).toEqual([]);
    });

    it("the same value stated twice", () => {
      expect(
        statedIdentityErrors([
          posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
          posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
        ]),
      ).toEqual([]);
    });

    it("a posting with no description", () => {
      // Nothing to be a contradiction about: the description is the key two
      // claims would have to share.
      expect(
        statedIdentityErrors([
          posting("", { type: IdentifierType.ISIN, value: "US0378331005" }),
          posting("", { type: IdentifierType.ISIN, value: "GB0000000001" }),
        ]),
      ).toEqual([]);
    });
  });
});

describe("the registry runs the check for every converter", () => {
  const contradicting: StandardParseResult = {
    postings: [
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "US0378331005" }),
      posting("APPLE INC COM", { type: IdentifierType.ISIN, value: "GB0000000001" }),
    ],
    periodFrom: new Date("2024-01-01"),
    periodBefore: new Date("2024-02-01"),
    errors: [],
  };

  beforeAll(() => {
    register({
      broker: Broker.IBKR,
      label: "Test",
      sourcePrefix: "TEST",
      formats: [
        {
          id: "test",
          label: "Test",
          accept: ".txt",
          // A converter that finds nothing wrong itself. The check is not its
          // to remember.
          convert: () => ({ ...contradicting, errors: [] }),
        },
      ],
    });
  });

  it("refuses a self-contradicting file the converter was happy with", () => {
    const convert = getBrokerEntry(Broker.IBKR)!.formats[0]!.convert!;
    const result = convert("anything");

    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("identifier_hints");
    // The postings are untouched: refusing is the upload modal's to do, on a
    // non-empty errors list, and a converter that dropped rows would report a
    // count nobody could reconcile with the file.
    expect(result.postings).toHaveLength(2);
  });
});
