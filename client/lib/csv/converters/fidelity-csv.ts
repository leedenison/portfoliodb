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
import type { Correlation, Posting } from "@/gen/archive/v1/txs_pb";
import { CorrelationSchema, PostingSchema } from "@/gen/archive/v1/txs_pb";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult, ParseError } from "@/lib/csv/parse-result";
import { parseCSVLine } from "@/lib/csv/line";
import {
  currencyHint,
  identifyRecord,
  reinvestIncomeLeg,
} from "@/lib/csv/postings";
import { mayBe, mustBe } from "@/lib/tx-type";
import { Big, parseDecimal } from "@/lib/decimal";

const ZERO = new Big(0);

/**
 * Fidelity exchange name to ISO 10383 MIC. These three are what the sample exports
 * use; a symbol whose exchange is not named here is passed on without a domain
 * rather than under a guess.
 */
const MIC_BY_EXCHANGE: Record<string, string> = {
  LON: "XLON",
  ETR: "XETR",
  EPA: "XPAR",
};

/**
 * The export's own asset column ("Type": CASH, ETF, STOCK, FUND) as a stated
 * asset class hint. FUND is an unlisted mutual fund. The hint routes
 * resolution; the canonical class still comes from the identifier plugins.
 */
const ASSET_CLASS_HINT: Record<string, AssetClass> = {
  STOCK: AssetClass.STOCK,
  ETF: AssetClass.ETF,
  FUND: AssetClass.MUTUAL_FUND,
};

const FIDELITY_DATE_FORMAT = /^(\d{1,2})\s+([A-Za-z]{3})\s+(\d{4})$/;
const ISO_DATE_FORMAT = /^(\d{4})-(\d{2})-(\d{2})$/;
const MONTHS: Record<string, number> = {
  Jan: 0, Feb: 1, Mar: 2, Apr: 3, May: 4, Jun: 5,
  Jul: 6, Aug: 7, Sep: 8, Oct: 9, Nov: 10, Dec: 11,
};

/**
 * Both date formats a Fidelity export uses.
 *
 * The download writes "10 Feb 2022" in the completion date and an ISO date in the
 * order date, and some exports are ISO throughout. Reading only the first rejected
 * every row of such a file, which is a whole export lost to a date format the
 * source itself uses in the column beside it.
 *
 * Local midnight either way, matching how a date with no time is read everywhere
 * else here.
 */
function parseFidelityDate(value: string): Date | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const iso = trimmed.match(ISO_DATE_FORMAT);
  if (iso) {
    const [, year, month, day] = iso;
    const d = new Date(parseInt(year!, 10), parseInt(month!, 10) - 1, parseInt(day!, 10));
    return Number.isNaN(d.getTime()) ? null : d;
  }
  const m = trimmed.match(FIDELITY_DATE_FORMAT);
  if (!m) return null;
  const [, day, monthStr, year] = m;
  const month = MONTHS[monthStr];
  if (month === undefined) return null;
  const d = new Date(parseInt(year!, 10), month, parseInt(day!, 10));
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * Broker transaction type strings this converter understands, each mapped to the
 * candidate set the row declares -- the most specific claim the broker's own
 * wording defends, a set where it defends more than one. A row whose type is
 * absent here is reported as a parse error and not converted; callers that
 * upload regardless use this map to name the types they dropped.
 */
export const FIDELITY_TYPE_TO_TYPES: Record<string, TxType[]> = {
  "Buy": [TxType.TRADE_ASSET],
  "Sell": [TxType.TRADE_ASSET],
  // Fidelity names a trade for the reason it happened. All of these are ordinary
  // trades that settle through a cash row of their own; only the reinvestment is
  // different, and it is different in kind rather than in name.
  "Buy From Dividend": [TxType.TRADE_ASSET],
  "Buy From Rebate": [TxType.TRADE_ASSET],
  "Buy For Switch": [TxType.TRADE_ASSET],
  "Sell For Switch": [TxType.TRADE_ASSET],
  // A compressed two-event group: the income buys the units without ever
  // arriving as money. The row is the trade's asset leg, and the dividend it
  // consumed is emitted beside it (see reinvestIncomeLeg), since the source
  // reports no row for it.
  "Reinvestment From Income": [TxType.TRADE_ASSET],
  "Cash Interest": [TxType.INTEREST],
  "Cash Dividend": [TxType.DIVIDEND],
  "Income Received": [TxType.INCOME],
  "Rebate": [TxType.INCOME],
  // A correction to interest already paid, and in the sample a credit of 0.02.
  "Interest Adjustment": [TxType.INTEREST],
  // Reverses a withdrawal in the same account on the same day, so it is the same
  // kind of movement as the row it undoes.
  "Adjustment": [TxType.TRANSFER],
  // Neither basis, custody nor margin, so the branch node is the whole claim.
  "Tax On Interest": [TxType.EXPENSE],
  // Per-trade charges belong in cost basis; the service fee is a platform cost
  // and does not. The leaves are cut by treatment, not by the broker's name.
  "Dealing Fee": [TxType.TRANSACTION_COST],
  "Service Fee": [TxType.HOLDING_COST],
  "Fx Charge": [TxType.TRANSACTION_COST],
  "PTM Levy": [TxType.TRANSACTION_COST],
  "Stamp Duty Or Financial Transaction Tax": [TxType.TRANSACTION_COST],
  "Withdrawal": [TxType.TRANSFER],
  "Transfer To Cash Management Account For Fees": [TxType.TRANSFER],
  "Transfer To Cash Management Account": [TxType.TRANSFER],
  "Transfer Out From Cash Management Account": [TxType.TRANSFER],
  "Transfer Into Account": [TxType.TRANSFER],
  "Cash In Ring-fenced For Fees": [TxType.TRANSFER],
  // Money arriving from outside the account -- except that `Cash In` also
  // settles the sale side of a switch, and the broker's wording cannot tell the
  // two apart. The set says exactly that: the row is a trade's cash leg or a
  // transfer, and which one is grouping's to decide, not this map's.
  "Cash In": [TxType.TRADE_CASH, TxType.TRANSFER],
  "Cash In Lump Sum": [TxType.TRANSFER],
  "Cash In For Transfer": [TxType.TRANSFER],
  // The cash leg of a trade, not a transfer: these pair 1:1 with their trade
  // rows and settle inside the account.
  "Cash In From Sell": [TxType.TRADE_CASH],
  "Cash Out": [TxType.TRADE_CASH],
  "Cash Out For Buy": [TxType.TRADE_CASH],
  "Cash Out For Buy From Transfer": [TxType.TRADE_CASH],
  "Cash Out For Dividend Reinvestment": [TxType.TRADE_CASH],
};

/**
 * Whether a declared set means the row's posting is money: the quantity is the
 * amount that moved, the description is the currency, and the posting carries a
 * currency on both sides.
 *
 * Every row that cannot be a trade's asset leg is money in this export --
 * income, charges, transfers and cash legs alike -- and a reinvestment is not,
 * though it names an income event: the posting is the units the income bought,
 * and reading its money total as a quantity would post the dividend as a share
 * count.
 */
export function isCashRow(types: readonly TxType[]): boolean {
  return !mayBe(types, TxType.TRADE_ASSET);
}

/**
 * The declared set a row carries once the asset it transacted is known.
 *
 * Fidelity models an account's cash as a tradable asset, so money leaving an
 * account is reported as a sale of cash and money arriving as a purchase of it.
 * Those rows carry `Buy` or `Sell` in the transaction type against an instrument
 * named "Cash", and the type map alone turns them into a security position sold
 * or bought in units of money.
 *
 * They are cash movements, and they settle inside the account against the
 * `Cash In From Sell` or `Cash Out For Buy From Transfer` row Fidelity reports
 * beside them, so they are the same TRADE_CASH as any other trade's cash leg.
 * The third row of the sequence -- a withdrawal or a transfer -- is the money
 * that actually moved, and it stands on its own.
 */
export function typeForAsset(types: TxType[], cashAsset: boolean): TxType[] {
  if (!cashAsset) return types;
  return mustBe(types, TxType.TRADE_ASSET) ? [TxType.TRADE_CASH] : types;
}

/**
 * The trade rows the export reports as an unsigned share count that left the
 * account.
 *
 * Direction is transcription rather than inference: the broker says "Sell" and the
 * quantity it prints is positive, so the sign has to be taken from the wording. What
 * these rows no longer decide is which cash row settles them.
 */
const SELL_ROWS = ["Sell", "Sell For Switch"];

/**
 * Widest reference-number gap tolerated between the row that opens a deposit and
 * a member of it.
 *
 * The run ascends from the `Cash In Lump Sum` and is usually consecutive. The
 * widest in the sample exports is 7, where a cash management account leg is
 * numbered inside the run. What actually separates one deposit from another is
 * the account, the amount agreeing to the penny, and the trade passes having
 * already taken the cash rows they need: every span from 7 to 1000 groups the
 * same 21 runs across both masters. So this is a guard against a distant
 * coincidence rather than a discriminator.
 */
const DEPOSIT_REF_SPAN = 8;

/**
 * The label the counterparty pointer is correlated under.
 *
 * Distinct from the reference series, which uses the empty label, so an account
 * name is never compared against a reference number. The pointer declares
 * MATCH_ACCOUNT alone: its token names the account of some other posting rather
 * than a token that posting carries, so equality against another token could
 * never fire.
 */
const COUNTERPARTY_LABEL = "counterparty";

/**
 * The correlation a Fidelity reference number supplies, or undefined for a row
 * the source gave no reference for.
 *
 * The cell verbatim as the token and the number it carries as the ordinal, which
 * is what MATCH_ORDINAL means: the ordinal field is populated and comparable,
 * never "parse the token as a number". A reference the source wrote in some shape
 * this converter did not expect keeps its equality half rather than becoming NaN.
 *
 * Scoped to the file. DEPOSIT_REF_SPAN travels with it because how densely a
 * broker issues references is a fact about its numbering rather than a grouping
 * policy, and a server-side constant would be wrong for every broker but one.
 *
 * Shared with the extension's JSON converter, so the two cannot disagree about
 * what a Fidelity reference is comparable by.
 */
export function fidelityRefCorrelation(ref: string): Correlation | undefined {
  if (!ref) return undefined;
  const ordinal = parseInt(ref, 10);
  if (!Number.isFinite(ordinal)) {
    return create(CorrelationSchema, {
      token: ref,
      scope: Scope.FILE,
      match: [Match.EXACT],
    });
  }
  return create(CorrelationSchema, {
    token: ref,
    ordinal: BigInt(ordinal),
    ordinalSpan: BigInt(DEPOSIT_REF_SPAN),
    scope: Scope.FILE,
    match: [Match.EXACT, Match.ORDINAL],
  });
}

/**
 * The correlation the account Fidelity names as the other side of a row
 * supplies.
 *
 * Scoped to the broker rather than to the file: an account label means nothing
 * outside the broker that issued it, and the two sides of one transfer routinely
 * arrive in different exports.
 */
export function fidelityCounterpartyCorrelation(account: string): Correlation {
  return create(CorrelationSchema, {
    label: COUNTERPARTY_LABEL,
    token: account,
    scope: Scope.BROKER,
    match: [Match.ACCOUNT],
  });
}

export function convertFidelityToStandard(
  csvText: string,
  options?: Record<string, unknown>
): StandardParseResult {
  const errors: ParseError[] = [];
  const currency = (options?.currency as string) ?? "";
  if (!currency) {
    return {
      postings: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: 0, field: "options", message: "Currency is required" }],
    };
  }

  const lines = csvText.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0);
  if (lines.length === 0) {
    return {
      postings: [],
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
      postings: [],
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
  const statusCol = col("status");
  const symbolCol = col("symbol");
  const exchangeCol = col("exchange");
  // The asset class of the row's instrument (CASH, ETF, STOCK, FUND), which is
  // what says whether a Buy or Sell transacted a security or the account's cash.
  // Distinct from "Transaction type"; an export without it falls back to the
  // instrument description, which is a required column.
  const assetClassCol = col("type");

  if (orderDateCol < 0 || txTypeCol < 0 || investmentsCol < 0) {
    return {
      postings: [],
      periodFrom: new Date(0),
      periodBefore: new Date(0),
      errors: [{ rowIndex: headerRowIndex + 1, field: "header", message: "Missing required Fidelity columns" }],
    };
  }

  const postings: Posting[] = [];
  // The rows whose units were bought with income, by posting index. A
  // reinvestment is the one row that produces a second posting here.
  const reinvestments: number[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  for (let i = headerRowIndex + 1; i < lines.length; i++) {
    const rowIndex = i + 1;
    const values = parseCSVLine(lines[i]);
    const get = (idx: number) => (idx >= 0 && idx < values.length ? values[idx].trim() : "");

    // A cancelled transaction reports zero units against zero value, so it adds
    // nothing, and its cash row is cancelled beside it. Skipping it also keeps a
    // trade that never happened from claiming a live trade's cash row.
    if (statusCol >= 0 && get(statusCol) === "Cancelled") continue;

    const completionDateStr = completionDateCol >= 0 ? get(completionDateCol) : "";
    const orderDateStr = get(orderDateCol);
    const dateStr = completionDateStr && completionDateStr !== "Pending" ? completionDateStr : orderDateStr;
    const date = parseFidelityDate(dateStr);
    if (!date) {
      errors.push({ rowIndex, field: "date", message: "Invalid or missing date" });
      continue;
    }

    const txTypeStr = get(txTypeCol);
    const mappedTypes = txTypeStr ? FIDELITY_TYPE_TO_TYPES[txTypeStr] : undefined;
    if (mappedTypes === undefined) {
      errors.push({ rowIndex, field: "type", message: txTypeStr ? `Unknown transaction type: ${txTypeStr}` : "Missing transaction type" });
      continue;
    }

    const investments = get(investmentsCol);
    const assetClass = assetClassCol >= 0 ? get(assetClassCol) : "";
    const cashAsset = assetClass ? assetClass === "CASH" : investments === "Cash";
    const rowTypes = typeForAsset(mappedTypes, cashAsset);
    const cashRow = isCashRow(rowTypes);

    // A cash posting is described by its currency, which is what resolves it to
    // the currency instrument rather than to a holding named after the broker's
    // wording. The export says "Cash" in the same column it names a security in,
    // so reading it through produced a security called Cash. See
    // docs/spec/archive-format.md.
    const instrumentDescription = cashRow ? currency : investments || txTypeStr;
    const account = accountCol >= 0 ? get(accountCol) : "";
    const qtyStr = get(qtyCol);
    const amountStr = amountCol >= 0 ? get(amountCol) : "";
    const amountDec = parseDecimal(amountStr);

    const rawQtyDec = parseDecimal(qtyStr);
    let quantity = rawQtyDec ?? ZERO;
    if (cashRow && amountDec !== undefined) {
      // Quantity is an unsigned magnitude: a fee and the interest that paid for
      // it both report a positive number, and the direction survives only in the
      // sign of Amount. Quantity is also 0 on some rows where money did move
      // (Tax On Interest reports 0 against an Amount of -0.20), so for cash the
      // transacted value is Amount itself rather than a sign-corrected Quantity.
      quantity = amountDec;
    } else if (SELL_ROWS.includes(txTypeStr)) {
      // Direction lives in the sign of quantity now, and the export reports an
      // unsigned share count, so a sale is negated by the broker's own wording.
      quantity = quantity.abs().times(-1);
    }
    const priceStr = priceCol >= 0 ? get(priceCol) : "";
    const unitPriceDec = parseDecimal(priceStr);

    // A cash posting resolves to its currency; a security one to its ticker, which
    // the export gives against an exchange name. A fund has no ticker and the export
    // repeats its name in the symbol column instead, which would resolve to nothing
    // and pollute the security master, so only a listed asset class offers one.
    const symbol = symbolCol >= 0 ? get(symbolCol) : "";
    const exchange = exchangeCol >= 0 ? get(exchangeCol) : "";
    const listed = assetClass !== "FUND";
    const identifierHints = cashRow
      ? currency
        ? [currencyHint(currency)]
        : []
      : symbol && exchange && listed
        ? [
            create(InstrumentRefSchema, {
              type: IdentifierType.MIC_TICKER,
              value: symbol,
              ...(MIC_BY_EXCHANGE[exchange] ? { domain: MIC_BY_EXCHANGE[exchange] } : {}),
            }),
          ]
        : [];

    const ts = date.getTime();
    if (ts < minTime) minTime = ts;
    if (ts > maxTime) maxTime = ts;

    if (txTypeStr === "Reinvestment From Income") reinvestments.push(postings.length);
    // The cell verbatim becomes the token and the number it carries becomes the
    // ordinal, in a field of its own: knowing how to take a number out of this
    // broker's references is exactly what a converter uniquely knows, and a
    // reference written in some other shape has to survive rather than become NaN.
    //
    // No counterparty correlation. The export's "Source investment" column holds
    // an asset name, not an account, so this file names no counterparty anywhere.
    // Only the JSON the extension reads does.
    const correlation = fidelityRefCorrelation(refCol >= 0 ? get(refCol) : "");
    postings.push(
      create(PostingSchema, {
        timestamp: timestampFromDate(date),
        instrumentDescription,
        brokerTxType: rowTypes,
        // A money row states CASH; a security row states what the export's own
        // asset column says, and nothing when the column is absent.
        ...(cashRow
          ? { assetClassHint: AssetClass.CASH }
          : ASSET_CLASS_HINT[assetClass]
            ? { assetClassHint: ASSET_CLASS_HINT[assetClass] }
            : {}),
        quantity: quantity.toString(),
        account,
        settlementCurrency: currency,
        ...(cashRow ? { tradingCurrency: currency } : {}),
        // Presence, not truthiness: a reported price of zero is a price.
        ...(unitPriceDec !== undefined ? { unitPrice: unitPriceDec.toString() } : {}),
        // The export's own Amount for the row, unsigned, on the rows where it is
        // not already the quantity. A cash row's quantity is this figure, so
        // repeating it there would put the same number on the posting twice;
        // a security row's quantity is a share count, and this is the second
        // independently transcribed number grouping identifies its cash leg by.
        ...(!cashRow && amountDec !== undefined
          ? { settlementAmount: amountDec.abs().toString() }
          : {}),
        ...(correlation ? { correlations: [correlation] } : {}),
        ...(identifierHints.length > 0 ? { identifierHints } : {}),
      })
    );
  }

  // A reinvestment is a compressed two-event group: the asset leg the source
  // reported plus the dividend it consumed, which is derived here because no row
  // exists for it. The income leg inherits the row's reference, which is what the
  // server puts the pair back together by; a row the export gave no reference has
  // nothing to inherit and gets a synthesised one.
  for (const i of reinvestments) {
    const income = reinvestIncomeLeg(identifyRecord(postings[i], String(i)));
    if (income) postings.push(income);
  }

  const periodFrom = minTime === Infinity ? new Date(0) : new Date(minTime);
  const periodBefore =
    maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime));

  return { postings, periodFrom, periodBefore, errors };
}
