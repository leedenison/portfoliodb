/**
 * CSV serializer/parser for EOD price export/import.
 */

import Papa from "papaparse";
import { create } from "@bufbuild/protobuf";
import { ImportPriceRowSchema, IdentifierTypeSchema } from "@/gen/api/v1/api_pb";
import type { ExportPriceRow, ImportPriceRow } from "@/gen/api/v1/api_pb";
import type { ParseError } from "./standard";
import { assetClassToStr, assetClassFromStr } from "@/lib/asset-class";

/** Valid identifier type names from the proto IdentifierType enum (excluding UNSPECIFIED). */
const VALID_IDENTIFIER_TYPES = new Set(
  IdentifierTypeSchema.values
    .filter((v) => v.number !== 0)
    .map((v) => v.name),
);

const HEADER = "identifier_type,identifier_value,identifier_domain,price_date,open,high,low,close,adjusted_close,volume,asset_class";

const REQUIRED_COLUMNS = new Set(["identifier_type", "identifier_value", "price_date", "close"]);

function fmtOptNum(v: number | undefined): string {
  return v === undefined ? "" : String(v);
}

function fmtOptBigint(v: bigint | undefined): string {
  return v === undefined ? "" : String(v);
}

/** Serialize ExportPriceRow[] to CSV text. */
export function pricesToCsv(rows: ExportPriceRow[], exportedAt?: Date): string {
  const data = rows.map((r) => [
    r.identifierType,
    r.identifierValue,
    r.identifierDomain,
    r.priceDate,
    fmtOptNum(r.open),
    fmtOptNum(r.high),
    fmtOptNum(r.low),
    String(r.close),
    fmtOptNum(r.adjustedClose),
    fmtOptBigint(r.volume),
    assetClassToStr(r.assetClass),
  ]);
  const csv = Papa.unparse({ fields: HEADER.split(","), data }, { newline: "\n" }) + "\n";
  if (exportedAt) {
    return `# exported_at=${exportedAt.toISOString()}\n${csv}`;
  }
  return csv;
}

export interface PriceParseResult {
  prices: ImportPriceRow[];
  errors: ParseError[];
  exportedAt?: Date;
}

/** Parse CSV text into ImportPriceRow[] with validation. */
export function csvToPrices(text: string): PriceParseResult {
  const prices: ImportPriceRow[] = [];
  const errors: ParseError[] = [];

  // Extract metadata from comment lines and strip them before parsing.
  let exportedAt: Date | undefined;
  const dataLines: string[] = [];
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (trimmed.startsWith("# exported_at=")) {
      const d = new Date(trimmed.slice("# exported_at=".length));
      if (!isNaN(d.getTime())) exportedAt = d;
    } else if (trimmed.startsWith("#")) {
      continue;
    } else {
      dataLines.push(line);
    }
  }
  const csvText = dataLines.join("\n");

  const parsed = Papa.parse<string[]>(csvText, { header: false, skipEmptyLines: true });
  const rows = parsed.data;
  if (rows.length === 0) {
    return { prices, errors, exportedAt };
  }

  const headerFields = rows[0].map((h) => h.trim().toLowerCase());
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
    return { prices, errors, exportedAt };
  }

  for (let i = 1; i < rows.length; i++) {
    const fields = rows[i];
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
    if (!VALID_IDENTIFIER_TYPES.has(identifierType)) {
      errors.push({ rowIndex, field: "identifier_type", message: `unknown identifier_type: ${identifierType}` });
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
      assetClass: assetClassFromStr(get("asset_class")),
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

  return { prices, errors, exportedAt };
}
