/**
 * OFX investment statement parser. Converts an OFX/QFX file into a
 * StandardParseResult compatible with the upload flow.
 *
 * Uses the SECLIST section to enrich transactions with security names
 * and identifier hints (ISIN, CUSIP, SEDOL, OCC for options).
 */

import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { startOfNextDay } from "@/lib/dates";
import type { Tx } from "@/gen/api/v1/api_pb";
import { InstrumentIdentifierSchema, TxSchema } from "@/gen/api/v1/api_pb";
import { IdentifierType, TxType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult, ParseError } from "@/lib/csv/standard";
import { counterLegs, feeLeg, refPrefix } from "@/lib/csv/postings";
import { Big, parseDecimal } from "@/lib/decimal";
import { parseOfxSgml } from "./sgml";

const ZERO = new Big(0);

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

/**
 * Read a decimal field from an object, returning undefined when it is absent or
 * malformed. OFX amounts are decimal text and stay text until they are needed as
 * a value, so no digit is lost to a float64 on the way in.
 */
function dec(obj: unknown, key: string): Big | undefined {
  return parseDecimal(str(obj, key));
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
      periodBefore: new Date(0),
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
      periodBefore: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "No transaction list found in OFX file" }],
      secList: emptySecList,
    };
  }

  // Period from DTSTART/DTEND. DTEND is nominally exclusive -- it is the value
  // a client sends as the next DTSTART -- but brokers are inconsistent about
  // it, and a DTEND that stops short silently leaves the last day's rows
  // outside the window they are meant to replace. Take whichever bound covers
  // the transactions actually in the file.
  const periodFrom = parseOfxDate(str(tranList, "DTSTART")) ?? new Date(0);
  const dtEnd = parseOfxDate(str(tranList, "DTEND")) ?? new Date(0);

  const txs: Tx[] = [];
  // Legs of transactions the source gave no FITID for, so they can be given a
  // synthesised group once every real ref is known. FITID is mandatory in OFX,
  // but without a group a trade and its cash leg would land as two groups and
  // neither would balance -- too quiet a failure to leave to the source.
  const unreferenced: Tx[][] = [];
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
      // The broker's own reference for the transaction, unique within the file,
      // so it names the group a security leg and its cash share.
      const fitId = str(invTran, "FITID");
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
      // UNITPRICE, COMMISSION and TOTAL are all quoted in CURSYM; CURRATE is the
      // rate back to the account's base currency and no figure here is in it.
      // So this is the settlement currency as well as the trading one, and a fee
      // derived from these fields needs no conversion first.
      const currencyEl = one(inner.CURRENCY);
      const tradingCurrency = str(currencyEl, "CURSYM") || acctCurrency;
      if (!tradingCurrency) {
        errors.push({ rowIndex: txIndex, field: "CURRENCY", message: "No trading currency on transaction or account" });
        continue;
      }

      // Quantity and price depend on transaction category.
      let quantity: string;
      let unitPrice: string | undefined;

      if (tag === "INCOME") {
        // Income: quantity = TOTAL (cash amount), price = 1.
        quantity = (dec(inner, "TOTAL") ?? ZERO).toString();
        unitPrice = "1";
      } else {
        quantity = (dec(inner, "UNITS") ?? ZERO).toString();
        unitPrice = dec(inner, "UNITPRICE")?.toString();
      }

      // Build identifier hints.  Cash transaction types use a CURRENCY hint
      // so the ingestion layer resolves to the cash instrument rather than
      // creating a holding from the security description.
      const isCashTx = tag === "INCOME";
      const identifierHints = isCashTx
        ? [{ type: IdentifierType.CURRENCY, value: tradingCurrency }]
        : buildIdentifierHints(uniqueId, uniqueIdType);

      const ts = timestampFromDate(date);
      const hintProtos = identifierHints.map((h) =>
        create(InstrumentIdentifierSchema, {
          type: h.type,
          value: h.value,
          canonical: false,
        }),
      );

      const legs: Tx[] = [];
      const security = create(TxSchema, {
        timestamp: ts,
        instrumentDescription: description,
        type: def.txType,
        quantity,
        account: acctId,
        tradingCurrency,
        settlementCurrency: tradingCurrency,
        groupRef: fitId,
        // The same id in both fields, saying two different things: which postings
        // are one event, and which statement record this one was transcribed from.
        // Only this leg carries the reference -- the cash and fee legs below are
        // derived from TOTAL and COMMISSION rather than from rows of their own.
        ...(fitId ? { brokerRef: fitId } : {}),
        ...(unitPrice !== undefined ? { unitPrice } : {}),
        ...(hintProtos.length > 0 ? { identifierHints: hintProtos } : {}),
      });
      legs.push(security);

      // Security buys/sells carry a TOTAL that represents the cash leg.
      // Emit a paired CASHFLOW transaction so each holding is updated by
      // its own transaction.
      if (def.invTag !== null) {
        const total = dec(inner, "TOTAL");
        // What the broker charged on the trade. TOTAL nets it against the
        // consideration -- 378 at 61.06 with 11.54034 of commission settles at
        // -23092.22034 -- so the consideration is TOTAL plus the charge, in
        // either direction. Deriving it that way rather than from
        // quantity * unitPrice keeps the two cash legs summing to the total the
        // broker reported. TAXES is a charge like COMMISSION and there is no
        // field to tell them apart downstream, so they are one posting.
        const charge = [dec(inner, "COMMISSION"), dec(inner, "TAXES")]
          .filter((v): v is Big => v !== undefined)
          .reduce((sum, v) => sum.plus(v.abs()), ZERO);

        if (total !== undefined && !total.eq(ZERO)) {
          const cashHints = [
            create(InstrumentIdentifierSchema, {
              type: IdentifierType.CURRENCY,
              value: tradingCurrency,
              canonical: false,
            }),
          ];
          legs.push(
            create(TxSchema, {
              timestamp: ts,
              instrumentDescription: description,
              type: TxType.CASHFLOW,
              // Decimal end to end now: the split is computed exactly and the
              // wire carries the result, so the two cash legs sum back to the
              // broker's total for any input.
              quantity: total.plus(charge).toString(),
              unitPrice: "1",
              account: acctId,
              tradingCurrency,
              settlementCurrency: tradingCurrency,
              groupRef: fitId,
              identifierHints: cashHints,
            }),
          );
        }

        const fee = feeLeg(security, charge);
        if (fee) legs.push(fee);
      }

      txs.push(...legs);
      if (!fitId) unreferenced.push(legs);
    }
  }

  if (unreferenced.length > 0) {
    const prefix = refPrefix(txs);
    unreferenced.forEach((legs, i) => {
      for (const leg of legs) leg.groupRef = `${prefix}${i}`;
    });
  }

  // The income a dividend came from and the expense a commission went to. After
  // the loop so a derived fee gets its counter-leg too.
  txs.push(...counterLegs(txs));

  txs.sort((a, b) =>
    Number(a.timestamp?.seconds ?? 0) - Number(b.timestamp?.seconds ?? 0),
  );

  const lastTx = txs[txs.length - 1]?.timestamp;
  const afterLastTx = lastTx ? startOfNextDay(timestampDate(lastTx)) : new Date(0);
  const periodBefore = afterLastTx > dtEnd ? afterLastTx : dtEnd;

  return { txs, periodFrom, periodBefore, errors, secList };
}
