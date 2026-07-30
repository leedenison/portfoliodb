import { describe, expect, it } from "vitest";
import { expandCoverage, isGlobal, validateCoverage } from "./coverage";
import type { CoverageDecl, InstrumentRef } from "./coverage";

function decl(overrides: Partial<CoverageDecl> = {}): CoverageDecl {
  return { from: "2024-01-01", before: "2024-02-01", rowIndex: 1, ...overrides };
}

function ref(value: string, type = "MIC_TICKER", domain = "XNAS"): InstrumentRef {
  return { identifierType: type, identifierValue: value, identifierDomain: domain };
}

describe("isGlobal", () => {
  it("is true when no identifier field is set", () => {
    expect(isGlobal(decl())).toBe(true);
  });

  it("is false when any identifier field is set", () => {
    expect(isGlobal(decl({ identifierValue: "AAPL" }))).toBe(false);
    expect(isGlobal(decl({ identifierDomain: "XNAS" }))).toBe(false);
  });
});

describe("validateCoverage", () => {
  it("accepts a global declaration", () => {
    expect(validateCoverage(decl())).toEqual([]);
  });

  it("accepts a specific declaration", () => {
    const errors = validateCoverage(
      decl({ identifierType: "MIC_TICKER", identifierValue: "AAPL", identifierDomain: "XNAS" }),
    );
    expect(errors).toEqual([]);
  });

  it("accepts a specific declaration with an empty domain", () => {
    const errors = validateCoverage(
      decl({ identifierType: "OCC", identifierValue: "AAPL250117P00185000" }),
    );
    expect(errors).toEqual([]);
  });

  it("rejects an unknown identifier type", () => {
    const errors = validateCoverage(decl({ identifierType: "SEDOL_ISH", identifierValue: "X" }));
    expect(errors).toHaveLength(1);
    expect(errors[0].field).toBe("identifier_type");
  });

  it("rejects a half-specified identifier rather than treating it as global", () => {
    const missingValue = validateCoverage(decl({ identifierType: "MIC_TICKER" }));
    expect(missingValue.map((e) => e.field)).toEqual(["identifier_value"]);

    const domainOnly = validateCoverage(decl({ identifierDomain: "XNAS" }));
    expect(domainOnly.map((e) => e.field)).toEqual(["identifier_type", "identifier_value"]);
  });

  it("rejects malformed dates", () => {
    expect(validateCoverage(decl({ from: "2024-1-1" })).map((e) => e.field)).toEqual(["from"]);
    expect(validateCoverage(decl({ before: "01/02/2024" })).map((e) => e.field)).toEqual(["before"]);
  });

  it("rejects an empty or inverted span", () => {
    const empty = validateCoverage(decl({ from: "2024-01-01", before: "2024-01-01" }));
    expect(empty.map((e) => e.field)).toEqual(["before"]);

    const inverted = validateCoverage(decl({ from: "2024-02-01", before: "2024-01-01" }));
    expect(inverted.map((e) => e.field)).toEqual(["before"]);
  });

  it("reports errors against the declaration's own row", () => {
    const errors = validateCoverage(decl({ from: "nonsense", rowIndex: 7 }));
    expect(errors[0].rowIndex).toBe(7);
  });
});

describe("expandCoverage", () => {
  it("fans a global out to every instrument", () => {
    const { resolved, errors } = expandCoverage(
      [decl()],
      [ref("AAPL"), ref("MSFT")],
    );
    expect(errors).toEqual([]);
    expect(resolved).toEqual([
      { ...ref("AAPL"), from: "2024-01-01", before: "2024-02-01" },
      { ...ref("MSFT"), from: "2024-01-01", before: "2024-02-01" },
    ]);
  });

  it("emits nothing when there is no declaration", () => {
    expect(expandCoverage([], [ref("AAPL")]).resolved).toEqual([]);
  });

  it("emits nothing for an instrument-free file", () => {
    expect(expandCoverage([decl()], []).resolved).toEqual([]);
  });

  it("lets a specific declaration override the global for that instrument only", () => {
    const { resolved } = expandCoverage(
      [
        decl(),
        decl({ ...ref("MSFT"), from: "2024-03-01", before: "2024-04-01", rowIndex: 2 }),
      ],
      [ref("AAPL"), ref("MSFT")],
    );
    expect(resolved).toEqual([
      { ...ref("AAPL"), from: "2024-01-01", before: "2024-02-01" },
      { ...ref("MSFT"), from: "2024-03-01", before: "2024-04-01" },
    ]);
  });

  it("keeps several spans for one instrument and still suppresses the global", () => {
    const { resolved } = expandCoverage(
      [
        decl(),
        decl({ ...ref("MSFT"), from: "2024-03-01", before: "2024-04-01", rowIndex: 2 }),
        decl({ ...ref("MSFT"), from: "2024-06-01", before: "2024-07-01", rowIndex: 3 }),
      ],
      [ref("MSFT")],
    );
    expect(resolved).toEqual([
      { ...ref("MSFT"), from: "2024-03-01", before: "2024-04-01" },
      { ...ref("MSFT"), from: "2024-06-01", before: "2024-07-01" },
    ]);
  });

  it("distinguishes instruments by their whole identifier triple", () => {
    const { resolved } = expandCoverage(
      [decl(), decl({ ...ref("AAPL", "MIC_TICKER", "XLON"), from: "2024-03-01", before: "2024-04-01", rowIndex: 2 })],
      [ref("AAPL", "MIC_TICKER", "XNAS"), ref("AAPL", "MIC_TICKER", "XLON")],
    );
    expect(resolved).toEqual([
      { ...ref("AAPL", "MIC_TICKER", "XNAS"), from: "2024-01-01", before: "2024-02-01" },
      { ...ref("AAPL", "MIC_TICKER", "XLON"), from: "2024-03-01", before: "2024-04-01" },
    ]);
  });

  it("emits one entry per instrument however many rows name it", () => {
    const { resolved } = expandCoverage([decl()], [ref("AAPL"), ref("AAPL"), ref("AAPL")]);
    expect(resolved).toHaveLength(1);
  });

  it("rejects a second global", () => {
    const { errors } = expandCoverage(
      [decl(), decl({ from: "2020-01-01", before: "2021-01-01", rowIndex: 4 })],
      [ref("AAPL")],
    );
    expect(errors).toHaveLength(1);
    expect(errors[0].rowIndex).toBe(4);
    expect(errors[0].message).toMatch(/one global/i);
  });

  it("keeps a specific declaration for an instrument the file has no rows for", () => {
    const { resolved, errors } = expandCoverage(
      [decl({ ...ref("TSLA"), rowIndex: 2 })],
      [ref("AAPL")],
    );
    expect(errors).toEqual([]);
    expect(resolved).toEqual([{ ...ref("TSLA"), from: "2024-01-01", before: "2024-02-01" }]);
  });
});
