/**
 * Converts Fidelity's transaction-history JSON into standard transactions.
 *
 * This is the payload behind the site's CSV export: the page fetches these rows
 * and posts them back to a formatting endpoint to produce the CSV a user
 * downloads. Reading them directly avoids that round trip and keeps three fields
 * the CSV discards -- isin, sedol, and an explicit debit/credit indicator.
 *
 * The transaction type map is shared with the CSV converter so the two cannot
 * disagree about what a broker type means.
 */

import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { startOfNextDay } from "../lib/dates";
import type { Tx } from "@/gen/api/v1/api_pb";
import { IdentifierType, InstrumentIdentifierSchema, TxSchema } from "@/gen/api/v1/api_pb";
import type { FidelityLeg } from "@/lib/csv/converters/fidelity-csv";
import {
  assignFidelityGroups,
  FIDELITY_TYPE_TO_OFX,
  isCashMovement,
  isCashTxType,
} from "@/lib/csv/converters/fidelity-csv";
import type { ParseError, StandardParseResult } from "@/lib/csv/standard";
import { parseSlashDate } from "../lib/dates";

/** The fields this converter reads. Fidelity sends many more. */
interface FidelityRow {
  accountNumber?: string;
  transactionType?: string;
  assetName?: string | null;
  isin?: string | null;
  sedol?: string | null;
  units?: number;
  valuation?: number;
  pricePerUnit?: number;
  currency?: string;
  status?: string;
  debitCreditIndicator?: string;
  /** Broker reference number; the CSV export's "Reference Number". */
  referenceId?: string;
  /** Order date. Always present. */
  dealDate?: string;
  /** Completion date. Absent while a transaction has not settled. */
  settlementDate?: string | null;
}

/**
 * Validates an ISIN's check digit.
 *
 * Fidelity gives cash positions pseudo-identifiers that look structurally like
 * ISINs -- AA00S0000000 with SEDOL S000000 -- and they appear on Buy and Sell
 * rows as well as cash ones, so the transaction type cannot be used to tell them
 * apart. They fail the check digit, and every genuine identifier in the sample
 * passes it, so validating is what keeps three fictional instruments out of the
 * security master.
 */
export function isValidIsin(value: string): boolean {
  if (!/^[A-Z]{2}[A-Z0-9]{9}\d$/.test(value)) return false;
  const digits = [...value]
    .map((c) => (c >= "A" && c <= "Z" ? String(c.charCodeAt(0) - 55) : c))
    .join("");
  let sum = 0;
  for (let i = 0; i < digits.length; i++) {
    let d = Number(digits[digits.length - 1 - i]);
    if (i % 2 === 1) d *= 2;
    if (d > 9) d -= 9;
    sum += d;
  }
  return sum % 10 === 0;
}

function fail(message: string): StandardParseResult {
  return {
    txs: [],
    periodFrom: new Date(0),
    periodBefore: new Date(0),
    errors: [{ rowIndex: 0, field: "file", message }],
  };
}

// options is unused here but is part of the BrokerConverter signature in types.ts.
export function convertFidelityJson(
  payload: string,
  _options?: Record<string, unknown>
): StandardParseResult {
  let rows: unknown;
  try {
    rows = JSON.parse(payload);
  } catch (e) {
    return fail(`Response was not JSON: ${e instanceof Error ? e.message : String(e)}`);
  }
  if (!Array.isArray(rows)) {
    return fail("Expected a JSON array of transactions");
  }

  const errors: ParseError[] = [];
  const txs: Tx[] = [];
  const legs: FidelityLeg[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  rows.forEach((raw, i) => {
    const rowIndex = i + 1;
    const row = raw as FidelityRow;

    // Cancelled transactions carry zero units and zero valuation, so they add
    // nothing. Skipping them also avoids resolving an instrument -- and paying a
    // plugin call -- for a trade that never happened.
    if (row.status === "Cancelled") return;

    const date = parseSlashDate(row.settlementDate || row.dealDate || "");
    if (!date) {
      errors.push({ rowIndex, field: "date", message: "Invalid or missing date" });
      return;
    }

    const typeStr = row.transactionType ?? "";
    const ofxType = typeStr ? FIDELITY_TYPE_TO_OFX[typeStr] : undefined;
    if (ofxType === undefined) {
      errors.push({
        rowIndex,
        field: "type",
        message: typeStr ? `Unknown transaction type: ${typeStr}` : "Missing transaction type",
      });
      return;
    }

    // units and valuation are both unsigned; direction lives only in the
    // indicator. For a cash movement the transacted value is the money, and
    // units is 0 on some rows where money did move, so valuation is used there.
    const magnitude = isCashMovement(ofxType) ? (row.valuation ?? 0) : (row.units ?? 0);
    const quantity = row.debitCreditIndicator === "DEBIT" ? -Math.abs(magnitude) : Math.abs(magnitude);

    const currency = row.currency ?? "";
    const isin = row.isin ?? "";
    const sedol = row.sedol ?? "";
    const identifierHints = isValidIsin(isin)
      ? [
          create(InstrumentIdentifierSchema, {
            type: IdentifierType.ISIN,
            value: isin,
            canonical: false,
          }),
          ...(sedol
            ? [
                create(InstrumentIdentifierSchema, {
                  type: IdentifierType.SEDOL,
                  value: sedol,
                  canonical: false,
                }),
              ]
            : []),
        ]
      : [];

    const ts = date.getTime();
    if (ts < minTime) minTime = ts;
    if (ts > maxTime) maxTime = ts;

    legs.push({
      type: typeStr,
      account: row.accountNumber ?? "",
      dateKey: row.settlementDate || row.dealDate || "",
      amount: row.valuation ?? 0,
      ref: parseInt(row.referenceId ?? "", 10),
    });
    txs.push(
      create(TxSchema, {
        timestamp: timestampFromDate(date),
        instrumentDescription: row.assetName || "Cash",
        type: ofxType,
        quantity,
        account: row.accountNumber ?? "",
        ...(currency ? { settlementCurrency: currency } : {}),
        ...(currency && isCashTxType(ofxType) ? { tradingCurrency: currency } : {}),
        ...(row.pricePerUnit ? { unitPrice: row.pricePerUnit } : {}),
        ...(identifierHints.length > 0 ? { identifierHints } : {}),
      })
    );
  });

  assignFidelityGroups(legs).forEach((ref, i) => {
    if (ref) txs[i].groupRef = ref;
  });

  return {
    txs,
    periodFrom: minTime === Infinity ? new Date(0) : new Date(minTime),
    periodBefore:
      maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime)),
    errors,
  };
}
