/**
 * Parser for the standard transaction CSV format.
 * See docs/spec/csv-format.md for the format specification.
 */

import Papa from "papaparse";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { startOfNextDay } from "@/lib/dates";
import { parseDecimal } from "@/lib/decimal";
import type { Tx } from "@/gen/api/v1/api_pb";
import {
  AccountType,
  IdentifierType,
  InstrumentIdentifierSchema,
  TxSchema,
  TxType,
} from "@/gen/api/v1/api_pb";

export interface ParseError {
  rowIndex: number;
  field: string;
  message: string;
}

/** Comment line carrying the share count basis, e.g. "# share_count_basis=2026-07-29". */
const SHARE_COUNT_BASIS_PREFIX = "# share_count_basis=";

export interface StandardParseResult {
  txs: Tx[];
  periodFrom: Date;
  /** Exclusive: local midnight after the last transaction's day. */
  periodBefore: Date;
  errors: ParseError[];
  /**
   * The share count the quantities and unit prices are denominated in, as
   * "YYYY-MM-DD".
   *
   * Omit for an as-traded source: each row reflects the splits that happened
   * before its own transaction date and nothing after it, which is what
   * brokers normally supply and what the server assumes. Set it only when the
   * source has post-adjusted historical rows for splits that happened *after*
   * the transaction -- then this is the date those quantities are current as
   * of, and the server will not apply those splits a second time.
   *
   * The converter declares the convention rather than undoing the arithmetic:
   * reversing it here would need the split table, which lives server-side, and
   * would store numbers the broker never printed. See
   * docs/spec/bitemporality.md.
   */
  shareCountBasis?: string;
}

const TX_TYPE_BY_NAME = new Map<string, TxType>(
  (Object.entries(TxType) as [string, number][]).filter(([, v]) => typeof v === "number").map(([k, v]) => [k, v as TxType])
);

const IDENTIFIER_TYPE_BY_NAME = new Map<string, IdentifierType>(
  (Object.entries(IdentifierType) as [string, number][])
    .filter(([k, v]) => typeof v === "number" && k !== "IDENTIFIER_TYPE_UNSPECIFIED")
    .map(([k, v]) => [k, v as IdentifierType])
);

const ACCOUNT_TYPE_BY_NAME = new Map<string, AccountType>(
  (Object.entries(AccountType) as [string, number][])
    .filter(([k, v]) => typeof v === "number" && k !== "UNSPECIFIED")
    .map(([k, v]) => [k, v as AccountType])
);

const EXCHANGE_TYPES = new Set(["MIC", "OPENFIGI"]);

function parseTxType(value: string): TxType | null {
  const trimmed = value.trim().toUpperCase();
  if (trimmed === "" || trimmed === "TX_TYPE_UNSPECIFIED") return null;
  return TX_TYPE_BY_NAME.get(trimmed) ?? null;
}

/**
 * Parse an account_type cell. An absent or empty value is USER: a row that says
 * nothing about what kind of leg it is is an ordinary broker account posting.
 * Returns null for a value that is not in the vocabulary, so the row can be
 * rejected rather than silently treated as USER.
 */
function parseAccountType(value: string): AccountType | null {
  const trimmed = value.trim().toUpperCase();
  if (trimmed === "") return AccountType.USER;
  return ACCOUNT_TYPE_BY_NAME.get(trimmed) ?? null;
}

function parseIdentifierType(value: string): IdentifierType | null {
  const trimmed = value.trim().toUpperCase();
  if (trimmed === "" || trimmed === "IDENTIFIER_TYPE_UNSPECIFIED") return null;
  return IDENTIFIER_TYPE_BY_NAME.get(trimmed) ?? null;
}

/** Parse a date string (YYYY-MM-DD or ISO 8601). Returns null if invalid. */
function parseDate(value: string): Date | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const date = new Date(trimmed);
  return Number.isNaN(date.getTime()) ? null : date;
}

/** Parse a single CSV row into fields, handling quoted fields and escaped quotes. */
export function parseCSVLine(line: string): string[] {
  const result = Papa.parse(line, { header: false });
  return (result.data[0] as string[]) ?? [];
}

/**
 * Parse standard-format CSV text into Tx array and period.
 * Header names are case-insensitive. Required: date (or timestamp), instrument_description, type, quantity.
 * Optional: trading_currency, settlement_currency, unit_price, account, symbol_type, symbol, exchange_type,
 * exchange, group_ref, account_type.
 */
export function parseStandardCSV(csvText: string): StandardParseResult {
  const errors: ParseError[] = [];
  // Metadata rides on comment lines, matching the price CSV's exported_at.
  let shareCountBasis: string | undefined;
  const lines: string[] = [];
  for (const raw of csvText.split(/\r?\n/)) {
    const line = raw.trim();
    if (line.length === 0) continue;
    if (line.startsWith(SHARE_COUNT_BASIS_PREFIX)) {
      const value = line.slice(SHARE_COUNT_BASIS_PREFIX.length).trim();
      if (/^\d{4}-\d{2}-\d{2}$/.test(value)) {
        shareCountBasis = value;
      } else {
        errors.push({ rowIndex: 0, field: "share_count_basis", message: `Invalid date "${value}": expected YYYY-MM-DD` });
      }
      continue;
    }
    if (line.startsWith("#")) continue;
    lines.push(line);
  }
  if (lines.length === 0) {
    return { txs: [], periodFrom: new Date(0), periodBefore: new Date(0), errors: [{ rowIndex: 0, field: "file", message: "File is empty or has no header" }] };
  }

  const headerRow = parseCSVLine(lines[0]);
  const headerLower = headerRow.map((h) => h.trim().toLowerCase().replace(/\s+/g, "_"));
  const col = (name: string): number => {
    const n = name.toLowerCase();
    const i = headerLower.indexOf(n);
    if (i >= 0) return i;
    const alt = n === "timestamp" ? "date" : n === "date" ? "timestamp" : null;
    if (alt) return headerLower.indexOf(alt);
    return -1;
  };
  const dateCol = col("date") >= 0 ? col("date") : col("timestamp");
  const descCol = col("instrument_description");
  const typeCol = col("type");
  const qtyCol = col("quantity");
  const tradingCurrencyCol = col("trading_currency");
  const settlementCurrencyCol = col("settlement_currency");
  const priceCol = col("unit_price");
  const accountCol = col("account");
  const symbolTypeCol = col("symbol_type");
  const symbolCol = col("symbol");
  const exchangeTypeCol = col("exchange_type");
  const exchangeCol = col("exchange");
  const groupRefCol = col("group_ref");
  const accountTypeCol = col("account_type");

  if (dateCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: date or timestamp" });
  if (descCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: instrument_description" });
  if (typeCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: type" });
  if (qtyCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: quantity" });
  if (errors.length > 0) return { txs: [], periodFrom: new Date(0), periodBefore: new Date(0), errors };

  const txs: Tx[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  for (let i = 1; i < lines.length; i++) {
    const rowIndex = i + 1; // 1-based for display; row 0 is header
    const values = parseCSVLine(lines[i]);
    const get = (idx: number) => (idx >= 0 && idx < values.length ? values[idx].trim() : "");

    const dateStr = get(dateCol);
    const date = parseDate(dateStr);
    if (!date) {
      errors.push({ rowIndex, field: "date", message: "Invalid or missing date" });
      continue;
    }

    const instrumentDescription = get(descCol);
    if (!instrumentDescription) {
      errors.push({ rowIndex, field: "instrument_description", message: "Required" });
      continue;
    }

    const typeStr = get(typeCol);
    const txType = parseTxType(typeStr);
    if (txType === null) {
      errors.push({ rowIndex, field: "type", message: typeStr ? "Unknown transaction type" : "Required" });
      continue;
    }

    // Decimal cells are format-checked and carried through as text: the column
    // is exact and so is the wire field, so nothing here has to be a number.
    const quantity = parseDecimal(get(qtyCol))?.toString();
    if (quantity === undefined) {
      errors.push({ rowIndex, field: "quantity", message: "Must be a number" });
      continue;
    }

    const tradingCurrency = tradingCurrencyCol >= 0 ? get(tradingCurrencyCol) || undefined : undefined;
    const settlementCurrency = settlementCurrencyCol >= 0 ? get(settlementCurrencyCol) || undefined : undefined;
    const unitPriceStr = get(priceCol);
    const unitPrice = parseDecimal(unitPriceStr)?.toString();
    if (unitPriceStr && unitPrice === undefined) {
      errors.push({ rowIndex, field: "unit_price", message: "Must be a number if present" });
      continue;
    }

    const account = accountCol >= 0 ? get(accountCol) : "";
    const groupRef = groupRefCol >= 0 ? get(groupRefCol) : "";

    const accountTypeStr = accountTypeCol >= 0 ? get(accountTypeCol) : "";
    const accountType = parseAccountType(accountTypeStr);
    if (accountType === null) {
      errors.push({ rowIndex, field: "account_type", message: "Unknown account type" });
      continue;
    }

    // Parse exchange_type + exchange into a domain for the identifier hint.
    let domain: string | undefined;
    const exchangeTypeStr = exchangeTypeCol >= 0 ? get(exchangeTypeCol) : "";
    const exchangeStr = exchangeCol >= 0 ? get(exchangeCol) : "";
    if (exchangeTypeStr && exchangeStr) {
      if (!EXCHANGE_TYPES.has(exchangeTypeStr.toUpperCase())) {
        errors.push({ rowIndex, field: "exchange_type", message: `Unknown exchange type: ${exchangeTypeStr}` });
        continue;
      }
      domain = exchangeStr;
    } else if (exchangeTypeStr && !exchangeStr) {
      errors.push({ rowIndex, field: "exchange", message: "exchange is required when exchange_type is present" });
      continue;
    } else if (!exchangeTypeStr && exchangeStr) {
      errors.push({ rowIndex, field: "exchange_type", message: "exchange_type is required when exchange is present" });
      continue;
    }

    // Parse symbol_type + symbol into an identifier hint.
    const identifierHints: Array<{ type: IdentifierType; value: string; domain?: string }> = [];
    const symbolTypeStr = symbolTypeCol >= 0 ? get(symbolTypeCol) : "";
    const symbolStr = symbolCol >= 0 ? get(symbolCol) : "";
    if (symbolTypeStr && symbolStr) {
      const idType = parseIdentifierType(symbolTypeStr);
      if (idType === null) {
        errors.push({ rowIndex, field: "symbol_type", message: `Unknown identifier type: ${symbolTypeStr}` });
        continue;
      }
      identifierHints.push({ type: idType, value: symbolStr, ...(domain ? { domain } : {}) });
    } else if (symbolTypeStr && !symbolStr) {
      errors.push({ rowIndex, field: "symbol", message: "symbol is required when symbol_type is present" });
      continue;
    }

    const ts = date.getTime();
    if (ts < minTime) minTime = ts;
    if (ts > maxTime) maxTime = ts;

    txs.push(
      create(TxSchema, {
        timestamp: timestampFromDate(date),
        instrumentDescription,
        type: txType,
        quantity,
        account,
        accountType,
        ...(groupRef ? { groupRef } : {}),
        ...(tradingCurrency ? { tradingCurrency } : {}),
        ...(settlementCurrency ? { settlementCurrency } : {}),
        ...(unitPrice !== undefined ? { unitPrice } : {}),
        ...(identifierHints.length > 0
          ? {
              identifierHints: identifierHints.map((h) =>
                create(InstrumentIdentifierSchema, { type: h.type, value: h.value, canonical: false, ...(h.domain ? { domain: h.domain } : {}) })
              ),
            }
          : {}),
      })
    );
  }

  const periodFrom = minTime === Infinity ? new Date(0) : new Date(minTime);
  const periodBefore =
    maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime));

  return { txs, periodFrom, periodBefore, errors, shareCountBasis };
}
