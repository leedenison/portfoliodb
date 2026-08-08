/**
 * CSV parser for EOD price import.
 *
 * The export side is gone: it writes an archive document now. This goes with it
 * when the import follows.
 */

import Papa from "papaparse";
import { create } from "@bufbuild/protobuf";
import { ImportPriceRowSchema, ImportCoverageSchema } from "@/gen/api/v1/api_pb";
import type { ImportPriceRow, ImportCoverage } from "@/gen/api/v1/api_pb";
import type { ParseError } from "./standard";
import { assetClassFromStr } from "@/lib/asset-class";
import { VALID_IDENTIFIER_TYPES } from "@/lib/identifiers";
import { expandCoverage, validateCoverage } from "@/lib/coverage";
import type { CoverageDecl, InstrumentRef } from "@/lib/coverage";

/** Comment line carrying the export's knowledge time. */
const EXPORTED_AT_PREFIX = "# exported_at=";

/**
 * Comment line declaring a half-open [from, before) coverage span, in one of
 * two forms:
 *
 *     # coverage=<from>,<before>
 *     # coverage=<identifier_type>,<identifier_value>,<identifier_domain>,<from>,<before>
 *
 * The first is global and applies to every instrument in the file; the second
 * names one instrument and overrides the global for it. The five-field form's
 * order matches the data columns.
 */
const COVERAGE_PREFIX = "# coverage=";

const REQUIRED_COLUMNS = new Set(["identifier_type", "identifier_value", "price_date", "close"]);

/** Matches the protovalidate pattern the decimal wire fields carry. */
const DECIMAL_RE = /^-?[0-9]+(\.[0-9]+)?$/;

export interface PriceParseResult {
  prices: ImportPriceRow[];
  /** One entry per instrument per span, with any global already expanded. */
  coverage: ImportCoverage[];
  errors: ParseError[];
  exportedAt?: Date;
}

/**
 * Parse one "# coverage=" line. Returns undefined and records an error when
 * the field count matches neither accepted form.
 */
function parseCoverageLine(body: string, rowIndex: number, errors: ParseError[]): CoverageDecl | undefined {
  const fields = body.split(",").map((f) => f.trim());
  if (fields.length === 2) {
    return { from: fields[0], before: fields[1], rowIndex };
  }
  if (fields.length === 5) {
    return {
      identifierType: fields[0],
      identifierValue: fields[1],
      identifierDomain: fields[2],
      from: fields[3],
      before: fields[4],
      rowIndex,
    };
  }
  errors.push({
    rowIndex,
    field: "coverage",
    message:
      `Expected "<from>,<before>" or ` +
      `"<identifier_type>,<identifier_value>,<identifier_domain>,<from>,<before>", got ${fields.length} fields`,
  });
  return undefined;
}

/** Parse CSV text into ImportPriceRow[] with validation. */
export function csvToPrices(text: string): PriceParseResult {
  const prices: ImportPriceRow[] = [];
  const errors: ParseError[] = [];

  // Extract metadata from comment lines and strip them before parsing. Errors
  // on these lines carry the file line number, since they have no data row.
  let exportedAt: Date | undefined;
  const coverageDecls: CoverageDecl[] = [];
  const dataLines: string[] = [];
  const fileLines = text.split("\n");
  for (let i = 0; i < fileLines.length; i++) {
    const trimmed = fileLines[i].trim();
    if (trimmed.startsWith(EXPORTED_AT_PREFIX)) {
      const d = new Date(trimmed.slice(EXPORTED_AT_PREFIX.length));
      if (!isNaN(d.getTime())) exportedAt = d;
    } else if (trimmed.startsWith(COVERAGE_PREFIX)) {
      const decl = parseCoverageLine(trimmed.slice(COVERAGE_PREFIX.length), i + 1, errors);
      if (decl) {
        const declErrors = validateCoverage(decl);
        if (declErrors.length > 0) errors.push(...declErrors);
        else coverageDecls.push(decl);
      }
    } else if (trimmed.startsWith("#")) {
      continue;
    } else {
      dataLines.push(fileLines[i]);
    }
  }
  const csvText = dataLines.join("\n");

  const parsed = Papa.parse<string[]>(csvText, { header: false, skipEmptyLines: true });
  const rows = parsed.data;
  if (rows.length === 0) {
    return { prices, coverage: [], errors, exportedAt };
  }

  const headerFields = rows[0].map((h) => h.trim().toLowerCase());
  const colIdx = new Map<string, number>();
  for (let i = 0; i < headerFields.length; i++) {
    colIdx.set(headerFields[i], i);
  }

  // Validate required columns exist. Only a missing column stops the parse --
  // a bad comment line is reported without discarding the rows.
  let missingColumns = false;
  for (const col of REQUIRED_COLUMNS) {
    if (!colIdx.has(col)) {
      errors.push({ rowIndex: 0, field: col, message: `missing required column: ${col}` });
      missingColumns = true;
    }
  }
  if (missingColumns) {
    return { prices, coverage: [], errors, exportedAt };
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

    // Decimal cells are validated against the wire format and carried through as
    // text. Parsing them into a JS number would round-trip an exact decimal
    // through a float64 and silently lose digits the NUMERIC column can hold --
    // which is the bug that makes export and import disagree.
    if (!closeStr || !DECIMAL_RE.test(closeStr)) {
      errors.push({ rowIndex, field: "close", message: `invalid close price: ${closeStr}` });
      continue;
    }

    const row = create(ImportPriceRowSchema, {
      identifierType,
      identifierValue,
      identifierDomain: get("identifier_domain"),
      priceDate,
      close: closeStr,
      assetClass: assetClassFromStr(get("asset_class")),
      currency: get("currency"),
    });

    let badDecimal = false;
    for (const [column, assign] of [
      ["open", (v: string) => (row.open = v)],
      ["high", (v: string) => (row.high = v)],
      ["low", (v: string) => (row.low = v)],
      ["adjusted_close", (v: string) => (row.adjustedClose = v)],
    ] as const) {
      const raw = get(column);
      if (!raw) continue;
      if (!DECIMAL_RE.test(raw)) {
        errors.push({ rowIndex, field: column, message: `invalid ${column}: ${raw}` });
        badDecimal = true;
        break;
      }
      assign(raw);
    }
    if (badDecimal) continue;

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

  // Resolve declarations against the instruments the rows actually name, so a
  // global fans out to one wire entry each.
  const instruments: InstrumentRef[] = prices.map((p) => ({
    identifierType: p.identifierType,
    identifierValue: p.identifierValue,
    identifierDomain: p.identifierDomain,
  }));
  const expanded = expandCoverage(coverageDecls, instruments);
  errors.push(...expanded.errors);
  const coverage = expanded.resolved.map((c) => create(ImportCoverageSchema, c));

  return { prices, coverage, errors, exportedAt };
}
