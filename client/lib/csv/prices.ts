/**
 * CSV serializer/parser for EOD price export/import.
 */

import { create } from "@bufbuild/protobuf";
import { ImportPriceRowSchema } from "@/gen/api/v1/api_pb";
import type { ExportPriceRow, ImportPriceRow } from "@/gen/api/v1/api_pb";
import type { ParseError } from "./standard";
import { parseCSVLine } from "./standard";

const HEADER = "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class";

const REQUIRED_COLUMNS = new Set(["identifier_type", "identifier_value", "price_date", "close"]);

/** Escape a CSV field: quote if it contains commas, quotes, or newlines. */
function escapeField(value: string): string {
  if (value.includes(",") || value.includes('"') || value.includes("\n")) {
    return '"' + value.replace(/"/g, '""') + '"';
  }
  return value;
}

function fmtOptNum(v: number | undefined): string {
  return v === undefined ? "" : String(v);
}

function fmtOptBigint(v: bigint | undefined): string {
  return v === undefined ? "" : String(v);
}

/** Serialize ExportPriceRow[] to CSV text. */
export function pricesToCsv(rows: ExportPriceRow[]): string {
  const lines = [HEADER];
  for (const r of rows) {
    lines.push([
      escapeField(r.identifierType),
      escapeField(r.identifierValue),
      escapeField(r.identifierDomain),
      r.priceDate,
      fmtOptNum(r.open),
      fmtOptNum(r.high),
      fmtOptNum(r.low),
      String(r.close),
      fmtOptNum(r.adjustedClose),
      fmtOptBigint(r.volume),
      escapeField(r.assetClass),
    ].join(","));
  }
  return lines.join("\n") + "\n";
}

export interface PriceParseResult {
  prices: ImportPriceRow[];
  errors: ParseError[];
}

/** Parse CSV text into ImportPriceRow[] with validation. */
export function csvToPrices(text: string): PriceParseResult {
  const prices: ImportPriceRow[] = [];
  const errors: ParseError[] = [];

  const lines = text.split(/\r?\n/).filter((l) => l.trim() !== "");
  if (lines.length === 0) {
    return { prices, errors };
  }

  const headerFields = parseCSVLine(lines[0]).map((h) => h.trim().toLowerCase());
  const colIdx = new Map<string, number>();
  for (let i = 0; i < headerFields.length; i++) {
    colIdx.set(headerFields[i], i);
  }

  // Validate required columns exist.
  for (const col of REQUIRED_COLUMNS) {
    if (!colIdx.has(col)) {
      errors.push({ rowIndex: 0, field: col, message: `missing required column: ${col}` });
    }
  }
  if (errors.length > 0) {
    return { prices, errors };
  }

  for (let i = 1; i < lines.length; i++) {
    const fields = parseCSVLine(lines[i]);
    const rowIndex = i + 1; // 1-based, header is row 1

    const get = (col: string): string => {
      const idx = colIdx.get(col);
      return idx !== undefined && idx < fields.length ? fields[idx].trim() : "";
    };

    const identifierType = get("identifier_type");
    const identifierValue = get("identifier_value");
    const priceDate = get("price_date");
    const closeStr = get("close");

    if (!identifierType) {
      errors.push({ rowIndex, field: "identifier_type", message: "identifier_type is required" });
      continue;
    }
    if (!identifierValue) {
      errors.push({ rowIndex, field: "identifier_value", message: "identifier_value is required" });
      continue;
    }
    if (!priceDate) {
      errors.push({ rowIndex, field: "price_date", message: "price_date is required" });
      continue;
    }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(priceDate)) {
      errors.push({ rowIndex, field: "price_date", message: `invalid date format: ${priceDate}` });
      continue;
    }

    const close = Number(closeStr);
    if (!closeStr || isNaN(close)) {
      errors.push({ rowIndex, field: "close", message: `invalid close price: ${closeStr}` });
      continue;
    }

    const row = create(ImportPriceRowSchema, {
      identifierType,
      identifierValue,
      identifierDomain: get("identifier_domain"),
      priceDate,
      close,
      assetClass: get("asset_class"),
    });

    const openStr = get("open");
    if (openStr) {
      const v = Number(openStr);
      if (isNaN(v)) {
        errors.push({ rowIndex, field: "open", message: `invalid open: ${openStr}` });
        continue;
      }
      row.open = v;
    }

    const highStr = get("high");
    if (highStr) {
      const v = Number(highStr);
      if (isNaN(v)) {
        errors.push({ rowIndex, field: "high", message: `invalid high: ${highStr}` });
        continue;
      }
      row.high = v;
    }

    const lowStr = get("low");
    if (lowStr) {
      const v = Number(lowStr);
      if (isNaN(v)) {
        errors.push({ rowIndex, field: "low", message: `invalid low: ${lowStr}` });
        continue;
      }
      row.low = v;
    }

    const adjStr = get("adjusted_close");
    if (adjStr) {
      const v = Number(adjStr);
      if (isNaN(v)) {
        errors.push({ rowIndex, field: "adjusted_close", message: `invalid adjusted_close: ${adjStr}` });
        continue;
      }
      row.adjustedClose = v;
    }

    const volStr = get("volume");
    if (volStr) {
      const v = Number(volStr);
      if (isNaN(v) || !Number.isInteger(v)) {
        errors.push({ rowIndex, field: "volume", message: `invalid volume: ${volStr}` });
        continue;
      }
      row.volume = BigInt(volStr);
    }

    prices.push(row);
  }

  return { prices, errors };
}
