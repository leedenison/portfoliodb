import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { ExportCoverageSchema, ExportPriceRowSchema } from "@/gen/api/v1/api_pb";
import { AssetClass } from "@/gen/type/v1/type_pb";
import type { ExportPriceRow } from "@/gen/api/v1/api_pb";
import { pricesToCsv, csvToPrices } from "./prices";

// Overrides are typed against the init shape, not Partial<ExportPriceRow>: the
// latter makes $typeName optional, which create() will not accept.
function makeRow(
  overrides: MessageInitShape<typeof ExportPriceRowSchema> = {},
): ExportPriceRow {
  return create(ExportPriceRowSchema, {
    identifierType: "ISIN",
    identifierValue: "US0378331005",
    identifierDomain: "",
    priceDate: "2024-01-15",
    close: "185.9",
    ...overrides,
  });
}

describe("pricesToCsv", () => {
  it("produces header + data rows", () => {
    const csv = pricesToCsv([makeRow()]);
    const lines = csv.trim().split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]).toBe(
      "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class,currency"
    );
    expect(lines[1]).toContain("ISIN");
    expect(lines[1]).toContain("US0378331005");
    expect(lines[1]).toContain("2024-01-15");
    expect(lines[1]).toContain("185.9");
  });

  it("prepends exported_at comment line when date provided", () => {
    const exportedAt = new Date("2025-07-15T10:30:00.000Z");
    const csv = pricesToCsv([makeRow()], exportedAt);
    const lines = csv.split("\n");
    expect(lines[0]).toBe("# exported_at=2025-07-15T10:30:00.000Z");
    expect(lines[1]).toBe(
      "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class,currency"
    );
  });

  it("omits comment line when no exportedAt provided", () => {
    const csv = pricesToCsv([makeRow()]);
    expect(csv.startsWith("#")).toBe(false);
  });

  it("includes optional fields when present", () => {
    const csv = pricesToCsv([
      makeRow({ open: "185.5", high: "186.2", low: "184.8", adjustedClose: "185.9", volume: 50000000n, assetClass: AssetClass.STOCK, currency: "USD" }),
    ]);
    const lines = csv.trim().split("\n");
    const fields = lines[1].split(",");
    expect(fields[4]).toBe("185.5"); // open
    expect(fields[5]).toBe("186.2"); // high
    expect(fields[6]).toBe("184.8"); // low
    expect(fields[8]).toBe("185.9"); // adjusted_close
    expect(fields[9]).toBe("50000000"); // volume
    expect(fields[10]).toBe("STOCK"); // asset_class
    expect(fields[11]).toBe("USD"); // currency
  });

  it("leaves optional fields empty when absent", () => {
    const csv = pricesToCsv([makeRow()]);
    const fields = csv.trim().split("\n")[1].split(",");
    expect(fields[4]).toBe(""); // open
    expect(fields[5]).toBe(""); // high
    expect(fields[6]).toBe(""); // low
    expect(fields[8]).toBe(""); // adjusted_close
    expect(fields[9]).toBe(""); // volume
  });

  it("quotes fields containing commas", () => {
    const csv = pricesToCsv([makeRow({ identifierValue: "APPLE, INC" })]);
    expect(csv).toContain('"APPLE, INC"');
  });

  it("handles empty array", () => {
    const csv = pricesToCsv([]);
    const lines = csv.trim().split("\n");
    expect(lines).toHaveLength(1); // header only
  });
});

describe("csvToPrices", () => {
  const HEADER = "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class,currency";

  it("parses valid CSV", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,2024-01-15,185.5,186.2,184.8,185.9,185.9,50000000`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(1);
    const p = result.prices[0];
    expect(p.identifierType).toBe("ISIN");
    expect(p.identifierValue).toBe("US0378331005");
    expect(p.identifierDomain).toBe("");
    expect(p.priceDate).toBe("2024-01-15");
    expect(p.open).toBe("185.5");
    expect(p.high).toBe("186.2");
    expect(p.low).toBe("184.8");
    expect(p.close).toBe("185.9");
    expect(p.adjustedClose).toBe("185.9");
    expect(p.volume).toBe(50000000n);
  });

  it("handles empty optional fields", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(1);
    const p = result.prices[0];
    expect(p.open).toBeUndefined();
    expect(p.high).toBeUndefined();
    expect(p.low).toBeUndefined();
    expect(p.adjustedClose).toBeUndefined();
    expect(p.volume).toBeUndefined();
  });

  it("reports error for missing required columns", () => {
    const csv = "identifier_type,price_date,close\nISIN,2024-01-15,100";
    const result = csvToPrices(csv);
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors[0].field).toBe("identifier_value");
    expect(result.prices).toHaveLength(0);
  });

  it("reports error for invalid date format", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,01/15/2024,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("price_date");
  });

  it("reports error for invalid close price", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,2024-01-15,,,,abc,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("close");
  });

  it("reports error for invalid optional numeric field", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,2024-01-15,abc,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("open");
  });

  it("reports error for missing identifier_type", () => {
    const csv = `${HEADER}\n,US0378331005,,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("identifier_type");
  });

  it("handles MIC_TICKER with domain", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].identifierType).toBe("MIC_TICKER");
    expect(result.prices[0].identifierDomain).toBe("XNAS");
  });

  it("handles BROKER_DESCRIPTION with domain", () => {
    const csv = `${HEADER}\nBROKER_DESCRIPTION,APPLE INC,Fidelity:web:fidelity-csv,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].identifierType).toBe("BROKER_DESCRIPTION");
    expect(result.prices[0].identifierDomain).toBe("Fidelity:web:fidelity-csv");
  });

  it("parses asset_class column", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,,STOCK`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].assetClass).toBe(AssetClass.STOCK);
  });

  it("handles empty asset_class", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].assetClass).toBe(AssetClass.ASSET_CLASS_UNSPECIFIED);
  });

  it("parses currency column", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,,STOCK,USD`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].currency).toBe("USD");
  });

  it("handles empty currency", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].currency).toBe("");
  });

  it("round-trips a decimal too precise for a float64", () => {
    // 0.1 + 0.2 is the textbook case, but a price column is worse: these values
    // have more significant digits than a float64 carries, so parsing them into
    // a JS number and printing them back changes the digits. Export and import
    // are a round-trip pair, so that is a bug rather than a rounding preference.
    const precise = "185.90000000000012345";
    const tiny = "0.000000000000001";
    const csv = pricesToCsv([makeRow({ close: precise, open: tiny })]);
    const result = csvToPrices(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].close).toBe(precise);
    expect(result.prices[0].open).toBe(tiny);
    // The float path these replaced: one loses digits, the other comes back in
    // exponent notation, which the wire format does not even accept.
    expect(String(Number(precise))).toBe("185.90000000000012");
    expect(String(Number(tiny))).toBe("1e-15");
  });

  it("round-trips through pricesToCsv and csvToPrices", () => {
    const original = [
      makeRow({ open: "185.5", high: "186.2", low: "184.8", adjustedClose: "185.9", volume: 50000000n, currency: "USD" }),
      makeRow({ identifierType: "MIC_TICKER", identifierValue: "AAPL", identifierDomain: "XNAS", priceDate: "2024-01-16", close: "186.5", currency: "GBP" }),
    ];
    const exportedAt = new Date("2025-07-15T10:30:00.000Z");
    const csv = pricesToCsv(original, exportedAt);
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(2);
    expect(result.exportedAt).toEqual(exportedAt);
    expect(result.prices[0].identifierType).toBe("ISIN");
    expect(result.prices[0].close).toBe("185.9");
    expect(result.prices[0].open).toBe("185.5");
    expect(result.prices[0].volume).toBe(50000000n);
    expect(result.prices[0].currency).toBe("USD");
    expect(result.prices[1].identifierType).toBe("MIC_TICKER");
    expect(result.prices[1].identifierDomain).toBe("XNAS");
    expect(result.prices[1].close).toBe("186.5");
    expect(result.prices[1].open).toBeUndefined();
    expect(result.prices[1].currency).toBe("GBP");
  });

  it("extracts exported_at from comment line", () => {
    const csv = `# exported_at=2025-07-15T10:30:00.000Z\n${HEADER}\nISIN,US0378331005,,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(1);
    expect(result.exportedAt).toEqual(new Date("2025-07-15T10:30:00.000Z"));
  });

  it("returns undefined exportedAt when no comment line", () => {
    const csv = `${HEADER}\nISIN,US0378331005,,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.exportedAt).toBeUndefined();
  });

  it("ignores unknown comment lines", () => {
    const csv = `# some other comment\n${HEADER}\nISIN,US0378331005,,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(1);
    expect(result.exportedAt).toBeUndefined();
  });

  it("handles empty input", () => {
    const result = csvToPrices("");
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(0);
  });

  it("rejects unknown identifier_type", () => {
    const csv = `${HEADER}\nTICKER,AAPL,XNAS,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("identifier_type");
    expect(result.errors[0].message).toContain("unknown identifier_type");
    expect(result.prices).toHaveLength(0);
  });

  it("handles multiple rows with mixed valid and invalid", () => {
    const csv = [
      HEADER,
      "ISIN,US0378331005,,2024-01-15,,,,185.9,,",
      "ISIN,,,2024-01-16,,,,100,,", // missing identifier_value
      "MIC_TICKER,GOOG,XNAS,2024-01-17,,,,200,,",
    ].join("\n");
    const result = csvToPrices(csv);
    expect(result.prices).toHaveLength(2);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].rowIndex).toBe(3); // row 3 (1-based, header=1)
  });
});

describe("csvToPrices coverage headers", () => {
  const HDR = "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume";
  const ROWS = [
    "MIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,",
    "MIC_TICKER,MSFT,XNAS,2024-01-15,,,,400.1,,",
  ];

  it("yields no coverage when the file declares none", () => {
    const result = csvToPrices([HDR, ...ROWS].join("\n"));
    expect(result.errors).toEqual([]);
    expect(result.coverage).toEqual([]);
  });

  it("expands a global declaration over every instrument in the file", () => {
    const csv = ["# coverage=2024-01-01,2024-02-01", HDR, ...ROWS].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.coverage).toHaveLength(2);
    expect(result.coverage.map((c) => c.identifierValue)).toEqual(["AAPL", "MSFT"]);
    expect(result.coverage[0].from).toBe("2024-01-01");
    expect(result.coverage[0].before).toBe("2024-02-01");
  });

  it("lets a specific declaration override the global", () => {
    const csv = [
      "# coverage=2024-01-01,2024-02-01",
      "# coverage=MIC_TICKER,MSFT,XNAS,2024-03-01,2024-04-01",
      HDR,
      ...ROWS,
    ].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.coverage).toEqual([
      expect.objectContaining({ identifierValue: "AAPL", from: "2024-01-01", before: "2024-02-01" }),
      expect.objectContaining({ identifierValue: "MSFT", from: "2024-03-01", before: "2024-04-01" }),
    ]);
  });

  it("accepts an empty domain in the specific form", () => {
    const csv = [
      "# coverage=OCC,NVDA250620P00110000,,2024-06-11,2025-01-18",
      HDR,
      "OCC,NVDA250620P00110000,,2024-06-11,,,,14.45,,",
    ].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.coverage).toHaveLength(1);
    expect(result.coverage[0].identifierDomain).toBe("");
  });

  it("coexists with exported_at and other comment lines", () => {
    const csv = [
      "# exported_at=2024-05-01T00:00:00.000Z",
      "# just a note",
      "# coverage=2024-01-01,2024-02-01",
      HDR,
      ...ROWS,
    ].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.exportedAt?.toISOString()).toBe("2024-05-01T00:00:00.000Z");
    expect(result.coverage).toHaveLength(2);
  });

  it("reads declarations written below the data rows", () => {
    const csv = [HDR, ...ROWS, "# coverage=2024-01-01,2024-02-01"].join("\n");
    expect(csvToPrices(csv).coverage).toHaveLength(2);
  });

  it("rejects a field count matching neither form, naming the file line", () => {
    const csv = [HDR, "# coverage=MIC_TICKER,AAPL,2024-01-01,2024-02-01", ...ROWS].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("coverage");
    expect(result.errors[0].rowIndex).toBe(2);
    expect(result.coverage).toEqual([]);
  });

  it("reports a bad declaration without discarding the price rows", () => {
    const csv = ["# coverage=2024-02-01,2024-01-01", HDR, ...ROWS].join("\n");
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("before");
    expect(result.prices).toHaveLength(2);
    expect(result.coverage).toEqual([]);
  });
});

describe("price CSV coverage round trip", () => {
  it("carries coverage spans through serialize and parse", () => {
    const coverage = [
      create(ExportCoverageSchema, {
        identifierType: "ISIN",
        identifierValue: "US0378331005",
        identifierDomain: "",
        from: "2024-01-15",
        before: "2024-02-01",
      }),
    ];
    const csv = pricesToCsv([makeRow()], new Date("2026-07-30T00:00:00.000Z"), coverage);
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.exportedAt?.toISOString()).toBe("2026-07-30T00:00:00.000Z");
    expect(result.coverage).toEqual([
      expect.objectContaining({
        identifierType: "ISIN",
        identifierValue: "US0378331005",
        identifierDomain: "",
        from: "2024-01-15",
        before: "2024-02-01",
      }),
    ]);
  });

  it("writes no coverage header when the export supplied none", () => {
    expect(pricesToCsv([makeRow()])).not.toContain("# coverage=");
  });
});

describe("pricesToCsv coverage compression", () => {
  const span = (value: string, from: string, before: string) =>
    create(ExportCoverageSchema, {
      identifierType: "MIC_TICKER",
      identifierValue: value,
      identifierDomain: "XNAS",
      from,
      before,
    });
  const row = (value: string, priceDate = "2024-02-01") =>
    makeRow({ identifierType: "MIC_TICKER", identifierValue: value, identifierDomain: "XNAS", priceDate });

  const coverageLines = (csv: string) =>
    csv.split("\n").filter((l) => l.startsWith("# coverage="));

  it("collapses a shared span to one global line", () => {
    const csv = pricesToCsv(
      [row("AAPL"), row("MSFT"), row("GOOG")],
      undefined,
      [span("AAPL", "2022-01-01", "2025-07-07"),
       span("MSFT", "2022-01-01", "2025-07-07"),
       span("GOOG", "2022-01-01", "2025-07-07")],
    );
    expect(coverageLines(csv)).toEqual(["# coverage=2022-01-01,2025-07-07"]);
  });

  it("writes the odd one out as an exception beside the global", () => {
    const csv = pricesToCsv(
      [row("AAPL"), row("MSFT"), row("ATVI")],
      undefined,
      [span("AAPL", "2022-01-01", "2025-07-07"),
       span("MSFT", "2022-01-01", "2025-07-07"),
       span("ATVI", "2021-12-31", "2023-10-14")],
    );
    expect(coverageLines(csv)).toEqual([
      "# coverage=2022-01-01,2025-07-07",
      "# coverage=MIC_TICKER,ATVI,XNAS,2021-12-31,2023-10-14",
    ]);
  });

  it("keeps both spans of a two-period instrument explicit, since a specific overrides the global outright", () => {
    const csv = pricesToCsv(
      [row("AAPL"), row("MSFT"), row("TSLA")],
      undefined,
      [span("AAPL", "2022-01-01", "2025-07-07"),
       span("MSFT", "2022-01-01", "2025-07-07"),
       span("TSLA", "2022-01-01", "2022-06-01"),
       span("TSLA", "2024-01-01", "2024-06-01")],
    );
    expect(coverageLines(csv)).toEqual([
      "# coverage=2022-01-01,2025-07-07",
      "# coverage=MIC_TICKER,TSLA,XNAS,2022-01-01,2022-06-01",
      "# coverage=MIC_TICKER,TSLA,XNAS,2024-01-01,2024-06-01",
    ]);
  });

  it("keeps a covered instrument with no rows explicit, since the global never reaches it", () => {
    const csv = pricesToCsv(
      [row("AAPL"), row("MSFT")],
      undefined,
      [span("AAPL", "2022-01-01", "2025-07-07"),
       span("MSFT", "2022-01-01", "2025-07-07"),
       span("DELISTED", "2022-01-01", "2025-07-07")],
    );
    expect(coverageLines(csv)).toEqual([
      "# coverage=2022-01-01,2025-07-07",
      "# coverage=MIC_TICKER,DELISTED,XNAS,2022-01-01,2025-07-07",
    ]);
  });

  it("writes no global when no span is shared, which would save nothing", () => {
    const csv = pricesToCsv(
      [row("AAPL"), row("MSFT")],
      undefined,
      [span("AAPL", "2022-01-01", "2023-01-01"),
       span("MSFT", "2024-01-01", "2025-01-01")],
    );
    expect(coverageLines(csv)).toEqual([
      "# coverage=MIC_TICKER,AAPL,XNAS,2022-01-01,2023-01-01",
      "# coverage=MIC_TICKER,MSFT,XNAS,2024-01-01,2025-01-01",
    ]);
  });

  it("round trips the compressed form back to one entry per instrument per span", () => {
    const coverage = [
      span("AAPL", "2022-01-01", "2025-07-07"),
      span("MSFT", "2022-01-01", "2025-07-07"),
      span("ATVI", "2021-12-31", "2023-10-14"),
    ];
    const csv = pricesToCsv([row("AAPL"), row("MSFT"), row("ATVI")], undefined, coverage);
    const result = csvToPrices(csv);
    expect(result.errors).toEqual([]);
    expect(result.coverage).toEqual([
      expect.objectContaining({ identifierValue: "AAPL", from: "2022-01-01", before: "2025-07-07" }),
      expect.objectContaining({ identifierValue: "MSFT", from: "2022-01-01", before: "2025-07-07" }),
      expect.objectContaining({ identifierValue: "ATVI", from: "2021-12-31", before: "2023-10-14" }),
    ]);
  });
});
