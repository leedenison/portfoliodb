/**
 * Charles Schwab broker SPLIT extractor.
 *
 * Schwab transaction CSVs encode a stock split as a single row of
 * Action = Stock Split with the additional shares received in the
 * Quantity column. The ratio is not present in the row, so the admin
 * Corporate Events page always prompts for split_from / split_to. The
 * extractor only captures the date, instrument symbol, and the share
 * delta so the UI can pre-fill the ratio from a holder portfolio.
 *
 * Schwab does not register a tx converter (formats: []) so this module
 * only contributes a split extractor.
 */

import Papa from "papaparse";
import { Broker } from "@/gen/api/v1/api_pb";
import type { ParseError } from "@/lib/csv/standard";
import { register } from "./registry";
import { dedupeSplits, type ExtractedSplit, type SplitParseResult } from "./splits";

type Row = Record<string, string>;

const SPLIT_ACTION_RE = /stock\s*split/i;

function normalizeKey(k: string): string {
  return k.trim().toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "");
}

function pick(row: Row, ...keys: string[]): string {
  for (const k of keys) {
    const v = row[k];
    if (v != null && v !== "") return v.trim();
  }
  return "";
}

function parseDate(s: string): string {
  const trimmed = s.trim();
  if (!trimmed) return "";
  // Schwab dates are commonly MM/DD/YYYY but some exports use YYYY-MM-DD.
  const slash = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(trimmed);
  if (slash) {
    const m = slash[1].padStart(2, "0");
    const d = slash[2].padStart(2, "0");
    return `${slash[3]}-${m}-${d}`;
  }
  const iso = /^(\d{4})-(\d{2})-(\d{2})/.exec(trimmed);
  if (iso) return `${iso[1]}-${iso[2]}-${iso[3]}`;
  const d = new Date(trimmed);
  if (Number.isNaN(d.getTime())) return "";
  const y = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${y}-${mm}-${dd}`;
}

function parseQty(s: string): number {
  // Strip commas and currency markers Schwab sometimes inserts.
  const cleaned = s.replace(/[, $]/g, "");
  const n = parseFloat(cleaned);
  return Number.isFinite(n) ? n : NaN;
}

/**
 * Extract SPLIT events from a Schwab transactions CSV. The split_from /
 * split_to fields are intentionally left undefined; the admin Corporate
 * Events preview UI is responsible for prompting and pre-filling.
 */
export function extractSchwabSplits(csvText: string): SplitParseResult {
  const errors: ParseError[] = [];
  const splits: ExtractedSplit[] = [];

  const parsed = Papa.parse<Record<string, string>>(csvText, {
    header: true,
    skipEmptyLines: true,
    transformHeader: normalizeKey,
  });

  if (parsed.errors.length > 0) {
    for (const e of parsed.errors) {
      errors.push({ rowIndex: (e.row ?? 0) + 1, field: "file", message: e.message });
    }
  }

  parsed.data.forEach((rawRow, idx) => {
    const rowIndex = idx + 2; // header is row 1
    const row: Row = {};
    for (const [k, v] of Object.entries(rawRow)) {
      row[k] = typeof v === "string" ? v : "";
    }

    const action = pick(row, "action", "type", "transaction");
    if (!action || !SPLIT_ACTION_RE.test(action)) return;

    const dateStr = pick(row, "date", "trade_date", "settled_date", "transaction_date");
    const exDate = parseDate(dateStr);
    if (!exDate) {
      errors.push({ rowIndex, field: "date", message: "Missing or invalid date for SPLIT row" });
      return;
    }

    const symbol = pick(row, "symbol", "ticker");
    const description = pick(row, "description", "instrument_description", "security") || symbol;
    if (!symbol && !description) {
      errors.push({ rowIndex, field: "symbol", message: "SPLIT row has no symbol or description" });
      return;
    }

    const qtyStr = pick(row, "quantity", "qty", "shares");
    const qty = parseQty(qtyStr);
    const account = pick(row, "account", "account_number") || undefined;

    splits.push({
      exDate,
      account,
      identifier: symbol
        ? { type: "MIC_TICKER", value: symbol }
        : { type: "MIC_TICKER", value: description },
      instrumentDescription: description,
      splitFrom: undefined,
      splitTo: undefined,
      deltaShares: Number.isFinite(qty) ? String(qty) : undefined,
      sourceRow: rowIndex,
    });
  });

  return { splits: dedupeSplits(splits), errors };
}

register({
  broker: Broker.SCHB,
  label: "Charles Schwab",
  sourcePrefix: "SCHB",
  formats: [],
  splitExtractors: [
    {
      id: "schwab-csv-splits",
      label: "Schwab CSV",
      accept: ".csv",
      extract: extractSchwabSplits,
    },
  ],
});
