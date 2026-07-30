/**
 * Fidelity CSV to standard format converter.
 *
 * Kept free of React so non-browser consumers (the import extension) can use it
 * without pulling in a UI framework. The options component and the registry
 * entry live in ./fidelity.
 */

import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { startOfNextDay } from "@/lib/dates";
import type { Tx } from "@/gen/api/v1/api_pb";
import { TxSchema, TxType } from "@/gen/api/v1/api_pb";
import type { StandardParseResult, ParseError } from "@/lib/csv/standard";
import { parseCSVLine } from "@/lib/csv/standard";

const FIDELITY_DATE_FORMAT = /^(\d{1,2})\s+([A-Za-z]{3})\s+(\d{4})$/;
const MONTHS: Record<string, number> = {
  Jan: 0, Feb: 1, Mar: 2, Apr: 3, May: 4, Jun: 5,
  Jul: 6, Aug: 7, Sep: 8, Oct: 9, Nov: 10, Dec: 11,
};

function parseFidelityDate(value: string): Date | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const m = trimmed.match(FIDELITY_DATE_FORMAT);
  if (!m) return null;
  const [, day, monthStr, year] = m;
  const month = MONTHS[monthStr];
  if (month === undefined) return null;
  const d = new Date(parseInt(year!, 10), month, parseInt(day!, 10));
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * Broker transaction type strings this converter understands. A row whose type
 * is absent here is reported as a parse error and not converted; callers that
 * upload regardless use this map to name the types they dropped.
 */
export const FIDELITY_TYPE_TO_OFX: Record<string, TxType> = {
  "Buy": TxType.BUYSTOCK,
  "Sell": TxType.SELLSTOCK,
  "Cash Interest": TxType.INCOME,
  "Cash Dividend": TxType.INCOME,
  "Tax On Interest": TxType.INVEXPENSE,
  "Dealing Fee": TxType.INVEXPENSE,
  "Service Fee": TxType.INVEXPENSE,
  "Fx Charge": TxType.INVEXPENSE,
  "PTM Levy": TxType.INVEXPENSE,
  "Stamp Duty Or Financial Transaction Tax": TxType.INVEXPENSE,
  "Withdrawal": TxType.JRNLFUND,
  "Transfer To Cash Management Account For Fees": TxType.TRANSFER,
  "Transfer To Cash Management Account": TxType.TRANSFER,
  "Transfer Out From Cash Management Account": TxType.TRANSFER,
  "Transfer Into Account": TxType.TRANSFER,
  "Cash In Ring-fenced For Fees": TxType.TRANSFER,
  "Cash In": TxType.JRNLFUND,
  "Cash In Lump Sum": TxType.JRNLFUND,
  "Cash In From Sell": TxType.JRNLFUND,
  "Cash In For Transfer": TxType.JRNLFUND,
  "Cash Out For Buy": TxType.JRNLFUND,
  "Cash Out For Buy From Transfer": TxType.JRNLFUND,
};

export function isCashTxType(type: TxType): boolean {
  return (
    type === TxType.INCOME ||
    type === TxType.INVEXPENSE ||
    type === TxType.REINVEST ||
    type === TxType.TRANSFER ||
    type === TxType.MARGININTEREST ||
    type === TxType.RETOFCAP
  );
}

/**
 * Types whose transacted item is cash rather than a security, so the transaction
 * quantity is the money that moved. JRNLFUND is included here but not in
 * isCashTxType, which governs the currency hint rather than the quantity.
 */
export function isCashMovement(type: TxType): boolean {
  return isCashTxType(type) || type === TxType.JRNLFUND;
}

export function convertFidelityToStandard(
  csvText: string,
  options?: Record<string, unknown>
): StandardParseResult {
  const errors: ParseError[] = [];
  const currency = (options?.currency as string) ?? "";
  if (!currency) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: 0, field: "options", message: "Currency is required" }],
    };
  }

  const lines = csvText.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0);
  if (lines.length === 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "File is empty" }],
    };
  }

  let headerRowIndex = -1;
  let headerRow: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const row = parseCSVLine(lines[i]);
    const first = row[0]?.trim() ?? "";
    if (first === "Order date" || first === "Completion date") {
      headerRowIndex = i;
      headerRow = row;
      break;
    }
  }
  if (headerRowIndex < 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "Could not find Fidelity data header (Order date)" }],
    };
  }

  const headerLower = headerRow.map((h) => h.trim().toLowerCase().replace(/\s+/g, "_"));
  const col = (name: string): number => {
    const n = name.toLowerCase().replace(/\s+/g, "_");
    return headerLower.indexOf(n);
  };
  const orderDateCol = col("order_date");
  const completionDateCol = col("completion_date");
  const txTypeCol = col("transaction_type");
  const investmentsCol = col("investments");
  const accountCol = col("account_number");
  const qtyCol = col("quantity");
  const amountCol = col("amount");
  const priceCol = col("price_per_unit");

  if (orderDateCol < 0 || txTypeCol < 0 || investmentsCol < 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: headerRowIndex + 1, field: "header", message: "Missing required Fidelity columns" }],
    };
  }

  const txs: Tx[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  for (let i = headerRowIndex + 1; i < lines.length; i++) {
    const rowIndex = i + 1;
    const values = parseCSVLine(lines[i]);
    const get = (idx: number) => (idx >= 0 && idx < values.length ? values[idx].trim() : "");

    const completionDateStr = completionDateCol >= 0 ? get(completionDateCol) : "";
    const orderDateStr = get(orderDateCol);
    const dateStr = completionDateStr && completionDateStr !== "Pending" ? completionDateStr : orderDateStr;
    const date = parseFidelityDate(dateStr);
    if (!date) {
      errors.push({ rowIndex, field: "date", message: "Invalid or missing date" });
      continue;
    }

    const txTypeStr = get(txTypeCol);
    const ofxType = txTypeStr ? FIDELITY_TYPE_TO_OFX[txTypeStr] : undefined;
    if (ofxType === undefined) {
      errors.push({ rowIndex, field: "type", message: txTypeStr ? `Unknown transaction type: ${txTypeStr}` : "Missing transaction type" });
      continue;
    }

    const instrumentDescription = get(investmentsCol) || "Cash";
    const account = accountCol >= 0 ? get(accountCol) : "";
    const qtyStr = get(qtyCol);
    const amountStr = amountCol >= 0 ? get(amountCol) : "";
    const amount = amountStr ? parseFloat(amountStr) : NaN;

    let quantity = parseFloat(qtyStr);
    if (Number.isNaN(quantity)) quantity = 0;
    if (isCashMovement(ofxType) && !Number.isNaN(amount)) {
      // Quantity is an unsigned magnitude: a fee and the interest that paid for
      // it both report a positive number, and the direction survives only in the
      // sign of Amount. Quantity is also 0 on some rows where money did move
      // (Tax On Interest reports 0 against an Amount of -0.20), so for cash the
      // transacted value is Amount itself rather than a sign-corrected Quantity.
      quantity = amount;
    } else if (ofxType === TxType.SELLSTOCK || ofxType === TxType.SELLMF || ofxType === TxType.SELLDEBT || ofxType === TxType.SELLOPT || ofxType === TxType.SELLOTHER) {
      quantity = -Math.abs(quantity);
    }
    const priceStr = priceCol >= 0 ? get(priceCol) : "";
    const unitPrice = priceStr ? parseFloat(priceStr) : undefined;

    const ts = date.getTime();
    if (ts < minTime) minTime = ts;
    if (ts > maxTime) maxTime = ts;

    txs.push(
      create(TxSchema, {
        timestamp: timestampFromDate(date),
        instrumentDescription,
        type: ofxType,
        quantity,
        account,
        settlementCurrency: currency,
        ...(isCashTxType(ofxType) ? { tradingCurrency: currency } : {}),
        ...(unitPrice !== undefined && !Number.isNaN(unitPrice) ? { unitPrice } : {}),
      })
    );
  }

  const periodFrom = minTime === Infinity ? new Date(0) : new Date(minTime);
  const periodBefore =
    maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime));

  return { txs, periodFrom, periodBefore, errors };
}
