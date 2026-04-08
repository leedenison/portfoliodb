/**
 * Register IBKR with OFX/QFX format support.
 * Adds IBKR-specific post-processing: maps CONID identifiers to OCC
 * option symbols using the SECLIST TICKER field.
 */

import { create } from "@bufbuild/protobuf";
import {
  Broker,
  IdentifierType,
  InstrumentIdentifierSchema,
} from "@/gen/api/v1/api_pb";
import type { ParseError, StandardParseResult } from "@/lib/csv/standard";
import { parseOfxStatement, parseOfxDate } from "@/lib/ofx/parser";
import { parseOfxSgml } from "@/lib/ofx/sgml";
import { register } from "./registry";
import { dedupeSplits, type ExtractedSplit, type SplitParseResult } from "./splits";

function convertIbkrOfx(text: string): StandardParseResult {
  const result = parseOfxStatement(text);

  // IBKR uses CONID (internal contract ID) for options. The SECLIST
  // carries the OCC-format TICKER for these contracts. Add OCC hints
  // for any transaction whose SECID has no standard identifier hints
  // but can be resolved via SECLIST.
  for (const tx of result.txs) {
    const hasStandardHint = tx.identifierHints.length > 0;
    if (hasStandardHint) continue;

    // Look up the instrument description in the SECLIST by matching
    // on secName (which is what we set as instrumentDescription).
    for (const [, sec] of result.secList) {
      if (sec.secName === tx.instrumentDescription && sec.uniqueIdType === "CONID" && sec.ticker) {
        tx.identifierHints.push(
          create(InstrumentIdentifierSchema, {
            type: IdentifierType.OCC,
            value: sec.ticker,
            canonical: false,
          }),
        );
        break;
      }
    }
  }

  return result;
}

// ── Split extraction ────────────────────────────────────────────────
//
// IBKR encodes splits in the OFX MEMO/NAME field as
//   "AMZN(US0231351067) SPLIT 20 FOR 1 (AMZN, AMAZON.COM INC, US0231351067)"
// We scan every transaction-like record under INVTRANLIST for that
// pattern, extract the ratio, and harvest the ex date and identifier.
//
// We also accept the structured OFX SPLIT element (NUMERATOR /
// DENOMINATOR), which is the spec-canonical form, when present.

const SPLIT_RATIO_RE = /SPLIT\s+(\d+(?:\.\d+)?)\s+FOR\s+(\d+(?:\.\d+)?)/i;
const ISIN_PAREN_RE = /\(([A-Z]{2}[A-Z0-9]{9}\d)\)/;

interface RawRecord {
  [key: string]: unknown;
}

function asObj(v: unknown): RawRecord | undefined {
  if (v == null || typeof v !== "object") return undefined;
  if (Array.isArray(v)) return v[0] as RawRecord | undefined;
  return v as RawRecord;
}

function asArr(v: unknown): RawRecord[] {
  if (v == null) return [];
  if (Array.isArray(v)) return v as RawRecord[];
  return [v as RawRecord];
}

function readStr(obj: unknown, key: string): string {
  const o = asObj(obj);
  if (!o) return "";
  const v = o[key];
  return typeof v === "string" ? v.trim() : "";
}

function findDate(...records: (RawRecord | undefined)[]): string {
  // Prefer DTTRADE on the inner INVTRAN; fall back to DTPOSTED/DTSETTLE.
  // Walk every supplied container and its INVTRAN child.
  const candidates: (RawRecord | undefined)[] = [];
  for (const r of records) {
    candidates.push(r, asObj(r?.INVTRAN));
  }
  for (const c of candidates) {
    if (!c) continue;
    for (const key of ["DTTRADE", "DTPOSTED", "DTSETTLE"]) {
      const s = readStr(c, key);
      if (s) {
        const d = parseOfxDate(s);
        if (d) return ymd(d);
      }
    }
  }
  return "";
}

function ymd(d: Date): string {
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function extractIsinFromText(text: string): string | undefined {
  const m = ISIN_PAREN_RE.exec(text);
  return m ? m[1] : undefined;
}

function ratioFromMemo(text: string): { from: string; to: string } | null {
  const m = SPLIT_RATIO_RE.exec(text);
  if (!m) return null;
  const to = parseFloat(m[1]);
  const from = parseFloat(m[2]);
  if (!Number.isFinite(from) || !Number.isFinite(to)) return null;
  if (from <= 0 || to <= 0) return null;
  return { from: m[2], to: m[1] };
}

/**
 * Extract SPLIT events from an IBKR OFX/QFX file. Scans every
 * transaction wrapper under INVTRANLIST for either a structured OFX
 * SPLIT element or a SPLIT N FOR M pattern in the MEMO/NAME field.
 */
export function extractIbkrSplits(text: string): SplitParseResult {
  const errors: ParseError[] = [];
  const splits: ExtractedSplit[] = [];

  let body: Record<string, unknown>;
  try {
    body = parseOfxSgml(text).body;
  } catch (e) {
    return {
      splits: [],
      errors: [{ rowIndex: 0, field: "file", message: e instanceof Error ? e.message : String(e) }],
    };
  }

  const stmtRs = asObj(
    (((body.OFX as RawRecord)?.INVSTMTMSGSRSV1 as RawRecord)?.INVSTMTTRNRS as RawRecord)?.INVSTMTRS,
  );
  if (!stmtRs) {
    return {
      splits: [],
      errors: [{ rowIndex: 0, field: "file", message: "No investment statement found in OFX file" }],
    };
  }

  const acctId = readStr(asObj(stmtRs.INVACCTFROM), "ACCTID");
  const tranList = asObj(stmtRs.INVTRANLIST);
  if (!tranList) {
    return {
      splits: [],
      errors: [{ rowIndex: 0, field: "file", message: "No transaction list found in OFX file" }],
    };
  }

  // Build SECLIST lookup so we can resolve the security name and ISIN.
  type SecInfo = { secName: string; ticker: string; uniqueId: string; uniqueIdType: string };
  const secList = new Map<string, SecInfo>();
  const seclistEl = asObj(((body.OFX as RawRecord)?.SECLISTMSGSRSV1 as RawRecord)?.SECLIST);
  if (seclistEl) {
    for (const tag of ["STOCKINFO", "OPTINFO", "MFINFO", "DEBTINFO", "OTHERINFO"]) {
      for (const entry of asArr(seclistEl[tag])) {
        const info = asObj(entry.SECINFO) ?? entry;
        const secId = asObj(info.SECID);
        const uid = readStr(secId, "UNIQUEID");
        if (!uid) continue;
        secList.set(uid, {
          secName: readStr(info, "SECNAME"),
          ticker: readStr(info, "TICKER"),
          uniqueId: uid,
          uniqueIdType: readStr(secId, "UNIQUEIDTYPE"),
        });
      }
    }
  }

  let rowIndex = 0;
  for (const [tag, raw] of Object.entries(tranList)) {
    if (tag === "DTSTART" || tag === "DTEND") continue;
    for (const wrapper of asArr(raw)) {
      rowIndex++;

      // Walk one level into INVBUY/INVSELL containers when present.
      const inner = asObj(wrapper.INVBUY) ?? asObj(wrapper.INVSELL) ?? wrapper;
      const invTran = asObj(inner.INVTRAN) ?? asObj(wrapper.INVTRAN);

      const memo = readStr(invTran ?? inner, "MEMO") || readStr(inner, "MEMO") || readStr(wrapper, "NAME");

      // Two extraction paths:
      //
      // 1. Structured OFX SPLIT element (canonical).
      // 2. SPLIT N FOR M in MEMO/NAME (IBKR style).
      let splitFrom: string | undefined;
      let splitTo: string | undefined;

      if (tag === "SPLIT") {
        const num = readStr(wrapper, "NUMERATOR") || readStr(inner, "NUMERATOR");
        const den = readStr(wrapper, "DENOMINATOR") || readStr(inner, "DENOMINATOR");
        if (num && den) {
          const numF = parseFloat(num);
          const denF = parseFloat(den);
          if (!Number.isFinite(numF) || !Number.isFinite(denF) || numF <= 0 || denF <= 0) {
            errors.push({ rowIndex, field: "SPLIT", message: `Invalid split ratio ${num}:${den}` });
            continue;
          }
          // OFX semantics: NUMERATOR/DENOMINATOR yields the new-shares-per-old-share
          // factor. SplitRow stores split_from / split_to with factor = to / from.
          splitFrom = den;
          splitTo = num;
        }
      }

      if (splitFrom == null && memo) {
        const r = ratioFromMemo(memo);
        if (r) {
          splitFrom = r.from;
          splitTo = r.to;
        }
      }

      if (splitFrom == null || splitTo == null) continue;

      const exDate = findDate(wrapper, inner);
      if (!exDate) {
        errors.push({ rowIndex, field: "DTTRADE", message: "Missing or invalid date for SPLIT record" });
        continue;
      }

      // Identifier resolution. Prefer ISIN found in MEMO; fall back to
      // SECID lookup against SECLIST; finally to the raw UNIQUEID.
      const secId = asObj(inner.SECID) ?? asObj(wrapper.SECID);
      const uniqueId = readStr(secId, "UNIQUEID");
      const uniqueIdType = readStr(secId, "UNIQUEIDTYPE").toUpperCase();
      const secInfo = uniqueId ? secList.get(uniqueId) : undefined;

      let idType = "";
      let idValue = "";
      const isinFromMemo = memo ? extractIsinFromText(memo) : undefined;
      if (isinFromMemo) {
        idType = "ISIN";
        idValue = isinFromMemo;
      } else if (uniqueIdType === "ISIN" || uniqueIdType === "CUSIP" || uniqueIdType === "SEDOL") {
        idType = uniqueIdType;
        idValue = uniqueId;
      } else if (secInfo?.ticker) {
        idType = "MIC_TICKER";
        idValue = secInfo.ticker;
      } else if (uniqueId) {
        // Last resort: emit raw uniqueId. The server import will try to
        // resolve it via the identifier plugins.
        idType = uniqueIdType || "MIC_TICKER";
        idValue = uniqueId;
      }

      if (!idValue) {
        errors.push({ rowIndex, field: "SECID", message: "Could not derive instrument identifier for SPLIT record" });
        continue;
      }

      const description = secInfo?.secName || memo || idValue;

      splits.push({
        exDate,
        account: acctId || undefined,
        identifier: { type: idType, value: idValue },
        instrumentDescription: description,
        splitFrom,
        splitTo,
        sourceRow: rowIndex,
      });
    }
  }

  return { splits: dedupeSplits(splits), errors };
}

register({
  broker: Broker.IBKR,
  label: "IBKR",
  sourcePrefix: "IBKR",
  formats: [
    {
      id: "ibkr-ofx",
      label: "OFX / QFX",
      accept: ".ofx,.qfx",
      convert: convertIbkrOfx,
    },
  ],
  splitExtractors: [
    {
      id: "ibkr-ofx-splits",
      label: "OFX / QFX",
      accept: ".ofx,.qfx",
      extract: extractIbkrSplits,
    },
  ],
});
