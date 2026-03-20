/**
 * Parser for the standard transaction CSV format.
 * See docs/ui/upload.md for the format specification.
 */

import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Tx } from "@/gen/api/v1/api_pb";
import {
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

export interface StandardParseResult {
  txs: Tx[];
  periodFrom: Date;
  periodTo: Date;
  errors: ParseError[];
}

const TX_TYPE_BY_NAME = new Map<string, TxType>(
  (Object.entries(TxType) as [string, number][]).filter(([, v]) => typeof v === "number").map(([k, v]) => [k, v as TxType])
);

function parseTxType(value: string): TxType | null {
  const trimmed = value.trim().toUpperCase();
  if (trimmed === "" || trimmed === "TX_TYPE_UNSPECIFIED") return null;
  return TX_TYPE_BY_NAME.get(trimmed) ?? null;
}

/** Parse a date string (YYYY-MM-DD or ISO 8601). Returns null if invalid. */
function parseDate(value: string): Date | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const date = new Date(trimmed);
  return Number.isNaN(date.getTime()) ? null : date;
}

/**
 * Parse a single CSV row into fields, handling quoted fields.
 * Assumes no newline inside a quoted value.
 */
export function parseCSVLine(line: string): string[] {
  const result: string[] = [];
  let current = "";
  let inQuotes = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inQuotes) {
      if (c === '"') {
        inQuotes = false;
      } else {
        current += c;
      }
    } else {
      if (c === '"') {
        inQuotes = true;
      } else if (c === ",") {
        result.push(current);
        current = "";
      } else {
        current += c;
      }
    }
  }
  result.push(current);
  return result;
}

/**
 * Parse standard-format CSV text into Tx array and period.
 * Header names are case-insensitive. Required: date (or timestamp), instrument_description, type, quantity.
 * Optional: trading_currency, settlement_currency, unit_price, account, exchange_code_hint (or exchange), mic_hint (or mic), isin, ticker, ticker_exchange (or ticker_domain), openfigi_share_class, occ.
 */
export function parseStandardCSV(csvText: string): StandardParseResult {
  const errors: ParseError[] = [];
  const lines = csvText.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0);
  if (lines.length === 0) {
    return { txs: [], periodFrom: new Date(0), periodTo: new Date(0), errors: [{ rowIndex: 0, field: "file", message: "File is empty or has no header" }] };
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
  const exchangeHintCol = col("exchange_code_hint") >= 0 ? col("exchange_code_hint") : col("exchange");
  const micHintCol = col("mic_hint") >= 0 ? col("mic_hint") : col("mic");
  const isinCol = col("isin");
  const tickerCol = col("ticker");
  const tickerExchangeCol = col("ticker_exchange") >= 0 ? col("ticker_exchange") : col("ticker_domain");
  const openfigiShareClassCol = col("openfigi_share_class");
  const occCol = col("occ");

  if (dateCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: date or timestamp" });
  if (descCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: instrument_description" });
  if (typeCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: type" });
  if (qtyCol < 0) errors.push({ rowIndex: 0, field: "header", message: "Missing required column: quantity" });
  if (errors.length > 0) return { txs: [], periodFrom: new Date(0), periodTo: new Date(0), errors };

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

    const qtyStr = get(qtyCol);
    const quantity = parseFloat(qtyStr);
    if (Number.isNaN(quantity)) {
      errors.push({ rowIndex, field: "quantity", message: "Must be a number" });
      continue;
    }

    const tradingCurrency = tradingCurrencyCol >= 0 ? get(tradingCurrencyCol) || undefined : undefined;
    const settlementCurrency = settlementCurrencyCol >= 0 ? get(settlementCurrencyCol) || undefined : undefined;
    const unitPriceStr = get(priceCol);
    const unitPrice = unitPriceStr ? parseFloat(unitPriceStr) : undefined;
    if (unitPriceStr && (Number.isNaN(unitPrice!) || unitPrice === undefined)) {
      errors.push({ rowIndex, field: "unit_price", message: "Must be a number if present" });
      continue;
    }

    const account = accountCol >= 0 ? get(accountCol) : "";
    const exchangeCodeHint = exchangeHintCol >= 0 ? get(exchangeHintCol) || undefined : undefined;
    const micHint = micHintCol >= 0 ? get(micHintCol) || undefined : undefined;

    const identifierHints: Array<{ type: IdentifierType; value: string; domain?: string }> = [];
    if (isinCol >= 0) {
      const v = get(isinCol);
      if (v) identifierHints.push({ type: IdentifierType.ISIN, value: v });
    }
    if (tickerCol >= 0) {
      const v = get(tickerCol);
      if (v) {
        const domain = tickerExchangeCol >= 0 ? get(tickerExchangeCol) || undefined : undefined;
        identifierHints.push({ type: IdentifierType.TICKER, value: v, ...(domain ? { domain } : {}) });
      }
    }
    if (openfigiShareClassCol >= 0) {
      const v = get(openfigiShareClassCol);
      if (v) identifierHints.push({ type: IdentifierType.OPENFIGI_SHARE_CLASS, value: v });
    }
    if (occCol >= 0) {
      const v = get(occCol);
      if (v) identifierHints.push({ type: IdentifierType.OCC, value: v });
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
        ...(tradingCurrency ? { tradingCurrency } : {}),
        ...(settlementCurrency ? { settlementCurrency } : {}),
        ...(unitPrice !== undefined && !Number.isNaN(unitPrice) ? { unitPrice } : {}),
        ...(exchangeCodeHint ? { exchangeCodeHint } : {}),
        ...(micHint ? { micHint } : {}),
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
  const periodTo = maxTime === -Infinity ? new Date(0) : new Date(maxTime);

  return { txs, periodFrom, periodTo, errors };
}
