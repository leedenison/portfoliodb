/**
 * OFX investment statement parser. Converts an OFX/QFX file into a
 * StandardParseResult compatible with the upload flow.
 *
 * Uses the SECLIST section to enrich transactions with security names
 * and identifier hints (ISIN, CUSIP, SEDOL, OCC for options).
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
import type { StandardParseResult, ParseError } from "@/lib/csv/standard";
import { parseOfxSgml } from "./sgml";

export interface OfxParseResult extends StandardParseResult {
  secList: Map<string, SecInfo>;
}

// ── Helpers ──────────────────────────────────────────────────────────

/** Safely navigate a nested object by dot-separated path. */
function dig(obj: unknown, ...keys: string[]): unknown {
  let cur = obj;
  for (const k of keys) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[k];
  }
  return cur;
}

/** Coerce a value to a single object (first element if array). */
function one(v: unknown): Record<string, unknown> | undefined {
  if (v == null) return undefined;
  if (Array.isArray(v)) return v[0] as Record<string, unknown>;
  return v as Record<string, unknown>;
}

/** Coerce a value to an array (wrap scalar, passthrough array). */
function many(v: unknown): Record<string, unknown>[] {
  if (v == null) return [];
  if (Array.isArray(v)) return v as Record<string, unknown>[];
  return [v as Record<string, unknown>];
}

/** Read a string field from an object. */
function str(obj: unknown, key: string): string {
  if (obj == null || typeof obj !== "object") return "";
  const v = (obj as Record<string, unknown>)[key];
  return typeof v === "string" ? v.trim() : "";
}

/** Read a numeric field from an object. */
function num(obj: unknown, key: string): number {
  const s = str(obj, key);
  if (!s) return NaN;
  return parseFloat(s);
}

// ── Date parsing ─────────────────────────────────────────────────────

/**
 * Parse OFX datetime: YYYYMMDDHHMMSS.fff[offset:TZ] or YYYYMMDD.
 * Returns a Date or null.
 */
export function parseOfxDate(value: string): Date | null {
  const s = value.trim();
  if (!s || s.length < 8) return null;

  const year = parseInt(s.slice(0, 4), 10);
  const month = parseInt(s.slice(4, 6), 10) - 1;
  const day = parseInt(s.slice(6, 8), 10);
  let hours = 0, minutes = 0, seconds = 0;

  if (s.length >= 14) {
    hours = parseInt(s.slice(8, 10), 10);
    minutes = parseInt(s.slice(10, 12), 10);
    seconds = parseInt(s.slice(12, 14), 10);
  }

  // Extract UTC offset from brackets: [-5:EST] -> -5
  const bracketMatch = s.match(/\[([+-]?\d+(?:\.\d+)?)/);
  if (bracketMatch) {
    const offsetHours = parseFloat(bracketMatch[1]);
    // Build an ISO string with the offset so Date parses it correctly.
    const sign = offsetHours >= 0 ? "+" : "-";
    const absH = Math.floor(Math.abs(offsetHours));
    const absM = Math.round((Math.abs(offsetHours) - absH) * 60);
    const iso =
      `${year}-${pad2(month + 1)}-${pad2(day)}T${pad2(hours)}:${pad2(minutes)}:${pad2(seconds)}` +
      `${sign}${pad2(absH)}:${pad2(absM)}`;
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? null : d;
  }

  // No offset -- treat as local time.
  const d = new Date(year, month, day, hours, minutes, seconds);
  return Number.isNaN(d.getTime()) ? null : d;
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

// ── SECLIST lookup ───────────────────────────────────────────────────

export interface SecInfo {
  secName: string;
  ticker: string;
  uniqueId: string;
  uniqueIdType: string;
}

/** Build a map from UNIQUEID -> SecInfo from the SECLIST section. */
function buildSecList(body: Record<string, unknown>): Map<string, SecInfo> {
  const map = new Map<string, SecInfo>();
  const secList = one(dig(body, "OFX", "SECLISTMSGSRSV1", "SECLIST"));
  if (!secList) return map;

  for (const tag of ["STOCKINFO", "OPTINFO", "MFINFO", "DEBTINFO", "OTHERINFO"]) {
    for (const entry of many(secList[tag])) {
      const info = one(entry.SECINFO) ?? entry;
      const secId = one(info.SECID);
      if (!secId) continue;
      const uid = str(secId, "UNIQUEID");
      if (!uid) continue;
      map.set(uid, {
        secName: str(info, "SECNAME"),
        ticker: str(info, "TICKER"),
        uniqueId: uid,
        uniqueIdType: str(secId, "UNIQUEIDTYPE"),
      });
    }
  }
  return map;
}

// ── Identifier hint mapping ──────────────────────────────────────────

const ID_TYPE_MAP: Record<string, IdentifierType> = {
  ISIN: IdentifierType.ISIN,
  CUSIP: IdentifierType.CUSIP,
  SEDOL: IdentifierType.SEDOL,
};

function buildIdentifierHints(
  uniqueId: string,
  uniqueIdType: string,
): { type: IdentifierType; value: string }[] {
  const hints: { type: IdentifierType; value: string }[] = [];

  const mapped = ID_TYPE_MAP[uniqueIdType.toUpperCase()];
  if (mapped !== undefined) {
    hints.push({ type: mapped, value: uniqueId });
  }

  return hints;
}

// ── Transaction type mapping ─────────────────────────────────────────

interface TxTypeDef {
  txType: TxType;
  /** Path from the wrapper element to the INVBUY/INVSELL/INVTRAN container. */
  invTag: "INVBUY" | "INVSELL" | null;
}

const TX_TYPES: Record<string, TxTypeDef> = {
  BUYSTOCK:  { txType: TxType.BUYSTOCK,  invTag: "INVBUY" },
  SELLSTOCK: { txType: TxType.SELLSTOCK, invTag: "INVSELL" },
  BUYOPT:    { txType: TxType.BUYOPT,    invTag: "INVBUY" },
  SELLOPT:   { txType: TxType.SELLOPT,   invTag: "INVSELL" },
  BUYMF:     { txType: TxType.BUYMF,     invTag: "INVBUY" },
  SELLMF:    { txType: TxType.SELLMF,    invTag: "INVSELL" },
  BUYDEBT:   { txType: TxType.BUYDEBT,   invTag: "INVBUY" },
  SELLDEBT:  { txType: TxType.SELLDEBT,  invTag: "INVSELL" },
  BUYOTHER:  { txType: TxType.BUYOTHER,  invTag: "INVBUY" },
  SELLOTHER: { txType: TxType.SELLOTHER, invTag: "INVSELL" },
  INCOME:    { txType: TxType.INCOME,    invTag: null },
  REINVEST:  { txType: TxType.REINVEST,  invTag: null },
  TRANSFER:  { txType: TxType.TRANSFER,  invTag: null },
};

// ── Main parser ──────────────────────────────────────────────────────

export function parseOfxStatement(text: string): OfxParseResult {
  const errors: ParseError[] = [];
  const emptySecList = new Map<string, SecInfo>();
  const { body } = parseOfxSgml(text);

  const stmtRs = one(
    dig(body, "OFX", "INVSTMTMSGSRSV1", "INVSTMTTRNRS", "INVSTMTRS"),
  );
  if (!stmtRs) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodTo: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "No investment statement found in OFX file" }],
      secList: emptySecList,
    };
  }

  const acctId = str(dig(stmtRs, "INVACCTFROM"), "ACCTID");
  const acctCurrency = str(stmtRs, "CURDEF");
  const secList = buildSecList(body);

  const tranList = one(stmtRs.INVTRANLIST);
  if (!tranList) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodTo: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "No transaction list found in OFX file" }],
      secList: emptySecList,
    };
  }

  // Period from DTSTART/DTEND.
  const periodFrom = parseOfxDate(str(tranList, "DTSTART")) ?? new Date(0);
  const periodTo = parseOfxDate(str(tranList, "DTEND")) ?? new Date(0);

  const txs: Tx[] = [];
  let txIndex = 0;

  for (const [tag, def] of Object.entries(TX_TYPES)) {
    for (const wrapper of many(tranList[tag])) {
      txIndex++;

      // Navigate to the inner container.
      const inner = def.invTag ? one(wrapper[def.invTag]) : wrapper;
      if (!inner) {
        errors.push({ rowIndex: txIndex, field: tag, message: `Missing ${def.invTag} element` });
        continue;
      }

      // INVTRAN holds FITID and DTTRADE.
      const invTran = one(inner.INVTRAN) ?? inner;
      const dateStr = str(invTran, "DTTRADE");
      const date = parseOfxDate(dateStr);
      if (!date) {
        errors.push({ rowIndex: txIndex, field: "DTTRADE", message: "Invalid or missing trade date" });
        continue;
      }

      // Security ID.
      const secId = one(inner.SECID);
      const uniqueId = str(secId, "UNIQUEID");
      const uniqueIdType = str(secId, "UNIQUEIDTYPE");
      const secInfo = uniqueId ? secList.get(uniqueId) : undefined;

      // Description from SECLIST, fallback to MEMO then UNIQUEID.
      const description =
        secInfo?.secName ||
        str(invTran, "MEMO") ||
        uniqueId ||
        tag;

      // Currency: per-transaction CURRENCY element, or account default.
      const currencyEl = one(inner.CURRENCY);
      const tradingCurrency = str(currencyEl, "CURSYM") || acctCurrency;

      // Quantity and price depend on transaction category.
      let quantity: number;
      let unitPrice: number | undefined;

      if (tag === "INCOME") {
        // Income: quantity = TOTAL (cash amount), price = 1.
        quantity = num(inner, "TOTAL");
        if (Number.isNaN(quantity)) quantity = 0;
        unitPrice = 1;
      } else {
        quantity = num(inner, "UNITS");
        if (Number.isNaN(quantity)) quantity = 0;
        const rawPrice = num(inner, "UNITPRICE");
        unitPrice = Number.isNaN(rawPrice) ? undefined : rawPrice;
      }

      // Build identifier hints.
      const identifierHints = buildIdentifierHints(uniqueId, uniqueIdType);

      txs.push(
        create(TxSchema, {
          timestamp: timestampFromDate(date),
          instrumentDescription: description,
          type: def.txType,
          quantity,
          account: acctId,
          tradingCurrency,
          ...(unitPrice !== undefined ? { unitPrice } : {}),
          ...(identifierHints.length > 0
            ? {
                identifierHints: identifierHints.map((h) =>
                  create(InstrumentIdentifierSchema, {
                    type: h.type,
                    value: h.value,
                    canonical: false,
                  }),
                ),
              }
            : {}),
        }),
      );
    }
  }

  txs.sort((a, b) =>
    Number(a.timestamp?.seconds ?? 0) - Number(b.timestamp?.seconds ?? 0),
  );

  return { txs, periodFrom, periodTo, errors, secList };
}
