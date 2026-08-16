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
import type { Correlation, Posting } from "@/gen/archive/v1/txs_pb";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { IdentifierType } from "@/gen/type/v1/type_pb";
import { AssetClass } from "@/gen/type/v1/type_pb";
import {
  fidelityCounterpartyCorrelation,
  fidelityRefCorrelation,
  FIDELITY_TYPE_TO_TYPES,
  isCashRow,
  typeForAsset,
} from "@/lib/csv/converters/fidelity-csv";
import type { ParseError, StandardParseResult } from "@/lib/csv/parse-result";
import {
  currencyHint,
  identifyRecord,
  reinvestIncomeLeg,
} from "@/lib/csv/postings";
import { Big, decimalFromNumber } from "@/lib/decimal";
import { parseSlashDate } from "../lib/dates";

const ZERO = new Big(0);

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
  /**
   * The account Fidelity names as the other side of this row. Populated on a
   * transfer arrival, naming where the money came from -- and also on a Service
   * Fee, where it names the product account the fee was charged for, which is
   * attribution rather than a transfer counterparty.
   *
   * Carried through either way, because the field says what the source said and
   * which of the two it means is not the converter's call. A Service Fee is a
   * holding cost whose group balances against an EXPENSE leg, so it never
   * produces the TRANSFER_CLEARING residual that transfer matching reads a
   * pointer for; the fee attribution is left for whatever wants it. See
   * docs/spec/postings.md.
   */
  sourceOrTargetAccount?: string | null;
}

/**
 * Validates an ISIN's check digit.
 *
 * Fidelity gives cash positions pseudo-identifiers that look structurally like
 * ISINs -- AA00S0000000 with SEDOL S000000 -- and they appear on Buy and Sell
 * rows as well as cash ones, so the transaction type cannot be used to tell them
 * apart. They fail the check digit, and every genuine identifier in the sample
 * passes it, so validating is what keeps three fictional instruments out of the
 * security master -- and, through typeForAsset, what stops a Buy or Sell of cash
 * being posted as a security trade.
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
    postings: [],
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
  const postings: Posting[] = [];
  // The rows whose units were bought with income, by posting index.
  const reinvestments: number[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  rows.forEach((raw, i) => {
    const rowIndex = i + 1;
    const row = raw as FidelityRow;

    // Cancelled transactions carry zero units and zero valuation, so they add
    // nothing. Skipping them also avoids resolving an instrument -- and paying a
    // plugin call -- for a trade that never happened.
    if (row.status === "Cancelled") return;

    // Both dates. The payload states them separately and they are two different
    // facts: the order date is what a trade and the charges levied on it agree
    // about, while the settlement date is when the trade actually completed.
    const orderDate = parseSlashDate(row.dealDate || "");
    if (!orderDate) {
      errors.push({ rowIndex, field: "dealDate", message: "Invalid or missing date" });
      return;
    }
    // An unsettled row states no settlement date, so the order date stands in
    // for it -- the only date the row carries.
    const tradeDate = parseSlashDate(row.settlementDate || "") ?? orderDate;

    const typeStr = row.transactionType ?? "";
    const mappedTypes = typeStr ? FIDELITY_TYPE_TO_TYPES[typeStr] : undefined;
    if (mappedTypes === undefined) {
      errors.push({
        rowIndex,
        field: "type",
        message: typeStr ? `Unknown transaction type: ${typeStr}` : "Missing transaction type",
      });
      return;
    }

    const isin = row.isin ?? "";
    // The check digit is what separates a cash pseudo-identifier from a real one,
    // so the same test that keeps them out of the security master says whether the
    // row transacted cash. There is no equivalent of the CSV export's Type column
    // here; a row carrying no identifier at all falls back to the asset name.
    const cashAsset = isin ? !isValidIsin(isin) : row.assetName === "Cash";
    const rowTypes = typeForAsset(mappedTypes, cashAsset);
    const cashRow = isCashRow(rowTypes);

    // units and valuation are both unsigned; direction lives only in the
    // indicator. For a cash movement the transacted value is the money, and
    // units is 0 on some rows where money did move, so valuation is used there.
    const magnitude = decimalFromNumber(cashRow ? row.valuation : row.units) ?? ZERO;
    const signed = magnitude.abs();
    const quantity = (row.debitCreditIndicator === "DEBIT" ? signed.times(-1) : signed).toString();

    const currency = row.currency ?? "";
    const sedol = row.sedol ?? "";
    // A cash movement resolves to its currency; anything else to the security its
    // identifiers name. The pseudo-identifiers a cash row carries would resolve to
    // a fictional instrument, so they are never passed on.
    const identifierHints = cashRow
      ? currency
        ? [currencyHint(currency)]
        : []
      : isValidIsin(isin)
        ? [
            create(InstrumentRefSchema, {
              type: IdentifierType.ISIN,
              value: isin,
            }),
            ...(sedol
              ? [
                  create(InstrumentRefSchema, {
                    type: IdentifierType.SEDOL,
                    value: sedol,
                  }),
                ]
              : []),
          ]
        : [];

    // The window is over the order date, which is what the replace period is
    // matched against and what a listing orders by.
    const ts = orderDate.getTime();
    if (ts < minTime) minTime = ts;
    if (ts > maxTime) maxTime = ts;

    // Two series, and they are kept apart: a reference number is comparable
    // against another reference number, while the counterparty names an account
    // and is compared against one. The builders are the CSV converter's, so the
    // two readings of a Fidelity export cannot disagree about what its
    // identifiers are comparable by.
    const correlations: Correlation[] = [];
    const refCorrelation = fidelityRefCorrelation(row.referenceId ?? "");
    if (refCorrelation) correlations.push(refCorrelation);
    if (row.sourceOrTargetAccount) {
      correlations.push(fidelityCounterpartyCorrelation(row.sourceOrTargetAccount));
    }

    if (typeStr === "Reinvestment From Income") reinvestments.push(postings.length);
    postings.push(
      create(PostingSchema, {
        orderDate: timestampFromDate(orderDate),
        tradeDate: timestampFromDate(tradeDate),
        // A cash posting is described by its currency, which is what resolves it
        // to the currency instrument rather than to a holding named after the
        // broker's wording -- the payload says "Cash" in the same field it names
        // a security in. See docs/spec/archive-format.md.
        instrumentDescription: cashRow ? currency || typeStr : row.assetName || typeStr,
        brokerTxType: rowTypes,
        // A money row states CASH; a security row's identity comes from its
        // ISIN, and the payload has no asset column to state a class from.
        ...(cashRow ? { assetClassHint: AssetClass.CASH } : {}),
        quantity,
        account: row.accountNumber ?? "",
        ...(currency ? { settlementCurrency: currency } : {}),
        ...(currency && cashRow ? { tradingCurrency: currency } : {}),
        // Presence, not truthiness: a reported price of zero is a price.
        ...(row.pricePerUnit !== undefined
          ? { unitPrice: (decimalFromNumber(row.pricePerUnit) ?? ZERO).toString() }
          : {}),
        // The payload's own valuation for the row, on the rows where it is not
        // already the quantity. A cash row's quantity is this figure; a security
        // row's is a share count, and this is the second independently
        // transcribed number grouping identifies its cash leg by.
        ...(!cashRow && row.valuation !== undefined
          ? {
              settlementAmount: (decimalFromNumber(row.valuation) ?? ZERO)
                .abs()
                .toString(),
            }
          : {}),
        ...(correlations.length > 0 ? { correlations } : {}),
        ...(identifierHints.length > 0 ? { identifierHints } : {}),
      })
    );
  });

  // A reinvestment's income leg is derived beside its trade, exactly as the CSV
  // converter does, and inherits the row's reference so the server can put the
  // pair back together.
  for (const i of reinvestments) {
    const income = reinvestIncomeLeg(identifyRecord(postings[i], String(i)));
    if (income) postings.push(income);
  }

  return {
    postings,
    periodFrom: minTime === Infinity ? new Date(0) : new Date(minTime),
    periodBefore:
      maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime)),
    errors,
  };
}
