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
import { counterLegs } from "@/lib/csv/postings";

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
  "Cash In For Transfer": TxType.JRNLFUND,
  // The cash leg of a trade, not a journal: these pair 1:1 with their trade rows
  // and settle inside the account. Typing them JRNLFUND made the whole trade
  // group read as a transfer, so its residual was routed to TRANSFER_CLEARING
  // rather than IMBALANCE and never reached the report that measures converter
  // lossiness. The types above keep JRNLFUND because their other side really is
  // outside the account.
  "Cash In From Sell": TxType.CASHFLOW,
  "Cash Out For Buy": TxType.CASHFLOW,
  "Cash Out For Buy From Transfer": TxType.CASHFLOW,
};

export function isCashTxType(type: TxType): boolean {
  return (
    type === TxType.INCOME ||
    type === TxType.INVEXPENSE ||
    type === TxType.REINVEST ||
    type === TxType.TRANSFER ||
    type === TxType.MARGININTEREST ||
    type === TxType.RETOFCAP ||
    type === TxType.CASHFLOW
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

/**
 * One row of a Fidelity export, reduced to what pairing needs. Shared by the CSV
 * and JSON converters so the two cannot disagree about how a trade is grouped.
 */
export interface FidelityLeg {
  /** Broker transaction type, verbatim (e.g. "Buy", "Cash In From Sell"). */
  type: string;
  account: string;
  /** The date both legs of a trade settle on, as the source spells it. */
  dateKey: string;
  /** Cash total for the row. Sign is ignored; only the magnitude pairs. */
  amount: number;
  /**
   * quantity * unit price for a trade row, or 0 when the source quoted neither.
   * Independent of `amount`, which is the broker's own total, so the two
   * disagreeing is evidence the legs do not belong together.
   */
  consideration: number;
  /** Broker reference number. NaN when the source did not supply one. */
  ref: number;
}

const AMOUNT_EPSILON = 0.005;

/**
 * Widest relative gap tolerated between a cash leg and its trade's
 * quantity * unit price.
 *
 * The two never agree exactly: the export rounds the unit price, so the product's
 * error grows with the quantity. Across the sample exports the worst correctly
 * paired trade is 0.16% out, so 0.5% clears every real pairing while rejecting a
 * swapped cash row, which is off by whole percentage points.
 *
 * This cannot replace the fee rule below. A fee gap is around 0.02% of a trade, far
 * inside this band, so two similar trades settling the same day would both pass it.
 */
const CONSIDERATION_BAND = 0.005;

/**
 * Assigns a group_ref to each leg, returning refs parallel to the input. An empty
 * string means the leg is its own single-posting group.
 *
 * Fidelity reports the cash side of a trade as a separate row, so the two legs are
 * paired here rather than a cash leg being derived, which would post the money
 * twice. Both rules are measured against the sample exports in local/masters: sells
 * pair 91/91 and buys 78/78.
 *
 * Charges (dealing fee, PTM levy, stamp duty, FX charge) are never grouped with a
 * trade. Fidelity dates them on the order date while the trade settles later, so
 * folding them in would misdate them, and their money is already accounted for by
 * their own rows.
 */
export function assignFidelityGroups(legs: FidelityLeg[]): string[] {
  const refs = legs.map(() => "");
  // NUL separator, written as an escape so this file stays plain text. A Fidelity
  // date is "10 Feb 2022", so a separator a date could contain risks two different
  // (account, date) pairs collapsing onto one key.
  const bucket = (l: FidelityLeg) => `${l.account}\u0000${l.dateKey}`;
  const indices = (type: string) =>
    legs.map((l, i) => (l.type === type && Number.isFinite(l.ref) ? i : -1)).filter((i) => i >= 0);

  const pair = (securityIdx: number, cashIdx: number) => {
    const ref = String(legs[securityIdx].ref);
    refs[securityIdx] = ref;
    refs[cashIdx] = ref;
  };

  // Both rules below match one broker-supplied total against another, so neither
  // notices a cash row that is internally consistent but belongs to a different
  // trade. quantity * unit price is derived from different fields, which makes it an
  // independent check: a swapped cash row misses it by percentage points.
  const consistent = (security: FidelityLeg, cash: FidelityLeg) => {
    const expected = Math.abs(security.consideration);
    // Nothing quoted to check against. Absence is not evidence of a bad pairing.
    if (expected === 0) return true;
    return Math.abs(Math.abs(cash.amount) - expected) / expected <= CONSIDERATION_BAND;
  };

  // Sells: the cash in equals the proceeds exactly, so the amount identifies it.
  const usedCash = new Set<number>();
  const cashIn = indices("Cash In From Sell");
  for (const s of indices("Sell")) {
    const match = cashIn.find(
      (c) =>
        !usedCash.has(c) &&
        bucket(legs[c]) === bucket(legs[s]) &&
        Math.abs(Math.abs(legs[c].amount) - Math.abs(legs[s].amount)) < AMOUNT_EPSILON &&
        consistent(legs[s], legs[c])
    );
    if (match !== undefined) {
      usedCash.add(match);
      pair(s, match);
    }
  }

  // Buys: the cash out is the consideration while the buy row's amount adds the
  // charges, so the amounts differ by a fee. A fee cannot be negative, which rules
  // out the cash row of a larger trade in the same bucket; among what survives, the
  // nearest reference number wins. Candidates are ranked globally rather than taken
  // in row order so that one buy cannot strand another by claiming its cash row.
  const buys = indices("Buy");
  const cashOut = indices("Cash Out For Buy");
  const candidates: Array<{ distance: number; buy: number; cash: number }> = [];
  for (const b of buys) {
    for (const c of cashOut) {
      if (bucket(legs[c]) !== bucket(legs[b])) continue;
      if (Math.abs(legs[b].amount) - Math.abs(legs[c].amount) < -AMOUNT_EPSILON) continue;
      if (!consistent(legs[b], legs[c])) continue;
      candidates.push({ distance: Math.abs(legs[b].ref - legs[c].ref), buy: b, cash: c });
    }
  }
  candidates.sort((x, y) => x.distance - y.distance || x.buy - y.buy || x.cash - y.cash);
  const usedBuy = new Set<number>();
  for (const { buy, cash } of candidates) {
    if (usedBuy.has(buy) || usedCash.has(cash)) continue;
    usedBuy.add(buy);
    usedCash.add(cash);
    pair(buy, cash);
  }

  return refs;
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
  const refCol = col("reference_number");

  if (orderDateCol < 0 || txTypeCol < 0 || investmentsCol < 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: headerRowIndex + 1, field: "header", message: "Missing required Fidelity columns" }],
    };
  }

  const txs: Tx[] = [];
  const legs: FidelityLeg[] = [];
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

    // dateStr, not the parsed date: both legs of a trade carry the same string,
    // and pairing only needs them to agree.
    // The raw quantity, not the sign-corrected one: cash rows overwrite it with
    // their amount, which would make the check compare a total against itself.
    const rawQty = parseFloat(qtyStr);
    legs.push({
      type: txTypeStr,
      account,
      dateKey: dateStr,
      amount: Number.isNaN(amount) ? 0 : amount,
      consideration:
        !Number.isNaN(rawQty) && unitPrice !== undefined && !Number.isNaN(unitPrice)
          ? Math.abs(rawQty * unitPrice)
          : 0,
      ref: refCol >= 0 ? parseInt(get(refCol), 10) : NaN,
    });
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

  assignFidelityGroups(legs).forEach((ref, i) => {
    if (ref) txs[i].groupRef = ref;
  });
  // After the refs are stamped, since that loop is index-parallel with legs.
  // Fidelity nets nothing into a trade total, so no fee is derived here: its
  // charges arrive as their own rows and only need the account they went to.
  txs.push(...counterLegs(txs));

  const periodFrom = minTime === Infinity ? new Date(0) : new Date(minTime);
  const periodBefore =
    maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime));

  return { txs, periodFrom, periodBefore, errors };
}
