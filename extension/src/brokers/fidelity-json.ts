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
import { InstrumentIdentifierSchema, TxSchema } from "@/gen/api/v1/api_pb";
import { IdentifierType } from "@/gen/type/v1/type_pb";
import type { FidelityLeg } from "@/lib/csv/converters/fidelity-csv";
import {
  assignFidelityGroups,
  asTradeCashLeg,
  FIDELITY_TYPE_TO_OFX,
  isCashMovement,
  isCashTxType,
  typeForAsset,
} from "@/lib/csv/converters/fidelity-csv";
import type { ParseError, StandardParseResult } from "@/lib/csv/standard";
import { counterLegs, currencyHint } from "@/lib/csv/postings";
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
   * which of the two it means is not the converter's call. A Service Fee is an
   * INVEXPENSE whose group balances against an EXPENSE leg, so it never produces
   * the TRANSFER_CLEARING residual that transfer matching reads a pointer for;
   * the fee attribution is left for whatever wants it. See docs/spec/postings.md.
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
    const mappedType = typeStr ? FIDELITY_TYPE_TO_OFX[typeStr] : undefined;
    if (mappedType === undefined) {
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
    const ofxType = typeForAsset(mappedType, cashAsset);

    // units and valuation are both unsigned; direction lives only in the
    // indicator. For a cash movement the transacted value is the money, and
    // units is 0 on some rows where money did move, so valuation is used there.
    const magnitude =
      decimalFromNumber(isCashMovement(ofxType) ? row.valuation : row.units) ?? ZERO;
    const signed = magnitude.abs();
    const quantity = (row.debitCreditIndicator === "DEBIT" ? signed.times(-1) : signed).toString();

    const currency = row.currency ?? "";
    const sedol = row.sedol ?? "";
    // A cash movement resolves to its currency; anything else to the security its
    // identifiers name. The pseudo-identifiers a cash row carries would resolve to
    // a fictional instrument, so they are never passed on.
    const identifierHints = isCashMovement(ofxType)
      ? currency
        ? [currencyHint(currency)]
        : []
      : isValidIsin(isin)
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

    // units, not the sign-corrected quantity: cash rows report their money as units,
    // which would make the check compare a total against itself.
    legs.push({
      type: typeStr,
      account: row.accountNumber ?? "",
      dateKey: row.settlementDate || row.dealDate || "",
      amount: row.valuation ?? 0,
      // The product is exact. Fidelity's JSON already decoded these through a
      // float64, so this is where they stop losing digits rather than where
      // they start being exact.
      consideration: (decimalFromNumber(row.units) ?? ZERO)
        .times(decimalFromNumber(row.pricePerUnit) ?? ZERO)
        .abs()
        .toNumber(),
      ref: parseInt(row.referenceId ?? "", 10),
      cashAsset,
    });
    txs.push(
      create(TxSchema, {
        timestamp: timestampFromDate(date),
        // A cash posting is described by its currency, which is what resolves it
        // to the currency instrument rather than to a holding named after the
        // broker's wording -- the payload says "Cash" in the same field it names
        // a security in. See docs/spec/csv-format.md.
        instrumentDescription: isCashMovement(ofxType)
          ? currency || typeStr
          : row.assetName || typeStr,
        type: ofxType,
        quantity,
        account: row.accountNumber ?? "",
        ...(currency ? { settlementCurrency: currency } : {}),
        ...(currency && isCashTxType(ofxType) ? { tradingCurrency: currency } : {}),
        // Presence, not truthiness: a reported price of zero is a price.
        ...(row.pricePerUnit !== undefined
          ? { unitPrice: (decimalFromNumber(row.pricePerUnit) ?? ZERO).toString() }
          : {}),
        // The string, not the number the leg carries: broker_ref is opaque, so a
        // reference the source wrote in some other shape survives rather than
        // becoming NaN.
        ...(row.referenceId ? { brokerRef: row.referenceId } : {}),
        ...(row.sourceOrTargetAccount ? { counterpartyAccount: row.sourceOrTargetAccount } : {}),
        ...(identifierHints.length > 0 ? { identifierHints } : {}),
      })
    );
  });

  const groups = assignFidelityGroups(legs);
  groups.refs.forEach((ref, i) => {
    if (ref) txs[i].groupRef = ref;
  });
  groups.cashLegs.forEach((paired, i) => {
    if (paired) asTradeCashLeg(txs[i], txs[i].settlementCurrency);
  });
  // After the refs are stamped, since that loop is index-parallel with legs.
  txs.push(...counterLegs(txs));

  return {
    txs,
    periodFrom: minTime === Infinity ? new Date(0) : new Date(minTime),
    periodBefore:
      maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime)),
    errors,
  };
}
