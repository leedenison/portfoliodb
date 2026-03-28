import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ExportPriceRowSchema } from "@/gen/api/v1/api_pb";
import type { ExportPriceRow } from "@/gen/api/v1/api_pb";
import { pricesToCsv, csvToPrices } from "./prices";

function makeRow(overrides: Partial<ExportPriceRow> = {}): ExportPriceRow {
  return create(ExportPriceRowSchema, {
    identifierType: "ISIN",
    identifierValue: "US0378331005",
    identifierDomain: "",
    priceDate: "2024-01-15",
    close: 185.9,
    ...overrides,
  });
}

describe("pricesToCsv", () => {
  it("produces header + data rows", () => {
    const csv = pricesToCsv([makeRow()]);
    const lines = csv.trim().split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]).toBe(
      "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class"
    );
    expect(lines[1]).toContain("ISIN");
    expect(lines[1]).toContain("US0378331005");
    expect(lines[1]).toContain("2024-01-15");
    expect(lines[1]).toContain("185.9");
  });

  it("includes optional fields when present", () => {
    const csv = pricesToCsv([
      makeRow({ open: 185.5, high: 186.2, low: 184.8, adjustedClose: 185.9, volume: 50000000n, assetClass: "STOCK" }),
    ]);
    const lines = csv.trim().split("\n");
    const fields = lines[1].split(",");
    expect(fields[4]).toBe("185.5"); // open
    expect(fields[5]).toBe("186.2"); // high
    expect(fields[6]).toBe("184.8"); // low
    expect(fields[8]).toBe("185.9"); // adjusted_close
    expect(fields[9]).toBe("50000000"); // volume
    expect(fields[10]).toBe("STOCK"); // asset_class
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
  const HEADER = "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class";

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
    expect(p.open).toBe(185.5);
    expect(p.high).toBe(186.2);
    expect(p.low).toBe(184.8);
    expect(p.close).toBe(185.9);
    expect(p.adjustedClose).toBe(185.9);
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
    expect(result.prices[0].assetClass).toBe("STOCK");
  });

  it("handles empty asset_class", () => {
    const csv = `${HEADER}\nMIC_TICKER,AAPL,XNAS,2024-01-15,,,,185.9,,`;
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices[0].assetClass).toBe("");
  });

  it("round-trips through pricesToCsv and csvToPrices", () => {
    const original = [
      makeRow({ open: 185.5, high: 186.2, low: 184.8, adjustedClose: 185.9, volume: 50000000n }),
      makeRow({ identifierType: "MIC_TICKER", identifierValue: "AAPL", identifierDomain: "XNAS", priceDate: "2024-01-16", close: 186.5 }),
    ];
    const csv = pricesToCsv(original);
    const result = csvToPrices(csv);
    expect(result.errors).toHaveLength(0);
    expect(result.prices).toHaveLength(2);
    expect(result.prices[0].identifierType).toBe("ISIN");
    expect(result.prices[0].close).toBe(185.9);
    expect(result.prices[0].open).toBe(185.5);
    expect(result.prices[0].volume).toBe(50000000n);
    expect(result.prices[1].identifierType).toBe("MIC_TICKER");
    expect(result.prices[1].identifierDomain).toBe("XNAS");
    expect(result.prices[1].close).toBe(186.5);
    expect(result.prices[1].open).toBeUndefined();
  });

  it("handles empty input", () => {
    const result = csvToPrices("");
    expect(result.errors).toHaveLength(0);
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
