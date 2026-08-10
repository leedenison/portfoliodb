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
import { IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult, ParseError } from "@/lib/csv/parse-result";
import { parseCSVLine } from "@/lib/csv/line";
import { counterLegs, currencyHint } from "@/lib/csv/postings";
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
 * Broker transaction type strings this converter understands. A row whose type
 * is absent here is reported as a parse error and not converted; callers that
 * upload regardless use this map to name the types they dropped.
 */
export const FIDELITY_TYPE_TO_OFX: Record<string, TxType> = {
  "Buy": TxType.BUYSTOCK,
  "Sell": TxType.SELLSTOCK,
  // Fidelity names a trade for the reason it happened. All of these are ordinary
  // trades that settle through a cash row of their own; only the reinvestment is
  // different, and it is different in kind rather than in name.
  "Buy From Dividend": TxType.BUYSTOCK,
  "Buy From Rebate": TxType.BUYSTOCK,
  "Buy For Switch": TxType.BUYSTOCK,
  "Sell For Switch": TxType.SELLSTOCK,
  // The only trade in the sample exports with no cash row anywhere beside it: the
  // income buys the units without ever arriving as money, which is what REINVEST
  // means. Its income leg is derived, since the source reports none.
  "Reinvestment From Income": TxType.REINVEST,
  "Cash Interest": TxType.INCOME,
  "Cash Dividend": TxType.INCOME,
  "Income Received": TxType.INCOME,
  "Rebate": TxType.INCOME,
  // A correction to interest already paid, and in the sample a credit of 0.02.
  "Interest Adjustment": TxType.INCOME,
  // Reverses a withdrawal in the same account on the same day, so it is the same
  // kind of movement as the row it undoes.
  "Adjustment": TxType.JRNLFUND,
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
  // Money arriving from outside the account. `Cash In` also settles the sale side
  // of a switch, which no type map can express: whether a row is a journal or a
  // trade's cash leg shows in whether it paired, so a paired one is retyped after
  // grouping. See asTradeCashLeg.
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
  "Cash Out": TxType.CASHFLOW,
  "Cash Out For Buy": TxType.CASHFLOW,
  "Cash Out For Buy From Transfer": TxType.CASHFLOW,
  "Cash Out For Dividend Reinvestment": TxType.CASHFLOW,
};

/**
 * Types whose posting is money, so it carries a currency on both sides.
 *
 * REINVEST is not one of them, though it names an income event: the posting is
 * the units the income bought, and the server weighs it at quantity times price
 * like any other security leg (transferTypes in
 * server/service/ingestion/balance.go). Reading its money total as a quantity
 * would post the dividend as a share count.
 */
export function isCashTxType(type: TxType): boolean {
  return (
    type === TxType.INCOME ||
    type === TxType.INVEXPENSE ||
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

/** Security sale types, whose quantity is a share count to be negated. */
const SELL_TYPES = new Set<TxType>([
  TxType.SELLSTOCK,
  TxType.SELLMF,
  TxType.SELLDEBT,
  TxType.SELLOPT,
  TxType.SELLOTHER,
]);

/** Security purchase types, the mirror of SELL_TYPES. */
const BUY_TYPES = new Set<TxType>([
  TxType.BUYSTOCK,
  TxType.BUYMF,
  TxType.BUYDEBT,
  TxType.BUYOPT,
  TxType.BUYOTHER,
]);

/**
 * The type a row carries once the asset it transacted is known.
 *
 * Fidelity models an account's cash as a tradable asset, so money leaving an
 * account is reported as a sale of cash and money arriving as a purchase of it.
 * Those rows carry `Buy` or `Sell` in the transaction type against an instrument
 * named "Cash", and the type map alone turns them into a security position sold
 * or bought in units of money.
 *
 * They are cash movements, and they settle inside the account against the
 * `Cash In From Sell` or `Cash Out For Buy From Transfer` row Fidelity reports
 * beside them, so they are the same CASHFLOW as any other trade's cash leg. The
 * third row of the sequence -- a withdrawal or a transfer -- is the money that
 * actually moved, and it stands on its own.
 */
export function typeForAsset(type: TxType, cashAsset: boolean): TxType {
  if (!cashAsset) return type;
  return BUY_TYPES.has(type) || SELL_TYPES.has(type) ? TxType.CASHFLOW : type;
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
  /**
   * Whether the row transacted cash rather than a security. A cash `Buy` or
   * `Sell` pairs against a different cash row than a security one does, so
   * pairing needs this as well as typeForAsset.
   */
  cashAsset: boolean;
}

const AMOUNT_EPSILON = 0.005;

/**
 * Widest relative gap tolerated between a cash leg and its trade's
 * quantity * unit price.
 *
 * The two never agree exactly: the export rounds the unit price, and the error that
 * leaves is the half-digit it dropped as a fraction of the price. That is worst on
 * a cheap unit, since the same half-digit is a larger share of a smaller number --
 * the widest correctly paired trade in the sample exports is a fund rebate of 9.24
 * units at a price printed as 0.85, 0.56% out. 0.75% clears every real pairing
 * while rejecting a swapped cash row, which is off by whole percentage points.
 *
 * This cannot replace the fee rule below. A fee gap is around 0.02% of a trade, far
 * inside this band, so two similar trades settling the same day would both pass it.
 */
const CONSIDERATION_BAND = 0.0075;

/** Trade rows whose cash leg is money coming in. */
const SELL_ROWS = ["Sell", "Sell For Switch"];

/** Trade rows whose cash leg is money going out. */
const BUY_ROWS = ["Buy", "Buy From Dividend", "Buy From Rebate", "Buy For Switch"];

/**
 * The rows a deposit into a product account is reported through, after the
 * `Cash In Lump Sum` that opens it.
 *
 * Money paid into a wrapper arrives as a run of three rows of the same amount:
 * the subscription is credited, spent, and credited again as the money that
 * lands. No security is involved -- the `Cash Out For Buy` here has no `Buy`
 * anywhere beside it -- and the three are one event, so they belong in one group.
 * Left ungrouped they are three single-posting groups, two of them JRNLFUND, so
 * the account reports twice the contribution it received and a residual the size
 * of the deposit lands in IMBALANCE.
 */
const DEPOSIT_ROWS = ["Cash Out For Buy", "Cash In"];

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

/**
 * The broker type of the cash row a trade settles through, or "" for a row that
 * is not a trade.
 *
 * Fidelity names the cash leg after the reason the trade happened rather than
 * after the trade, so each variant settles through a row type of its own. A
 * purchase of the account's own cash is the one that also turns on the asset: it
 * arrives through a transfer, and the broker says so in the name.
 */
function cashLegType(leg: FidelityLeg): string {
  switch (leg.type) {
    case "Sell":
      return "Cash In From Sell";
    case "Sell For Switch":
      return "Cash In";
    case "Buy":
      return leg.cashAsset ? "Cash Out For Buy From Transfer" : "Cash Out For Buy";
    case "Buy From Dividend":
      return "Cash Out For Dividend Reinvestment";
    case "Buy From Rebate":
    case "Buy For Switch":
      return "Cash Out";
    default:
      return "";
  }
}

/** What assignFidelityGroups worked out about each leg, parallel to its input. */
export interface FidelityGroups {
  /** Group ref. An empty string means the leg is its own single-posting group. */
  refs: string[];
  /** Whether the leg was paired as a trade's cash leg. */
  cashLegs: boolean[];
}

/**
 * Assigns a group_ref to each leg.
 *
 * Fidelity reports the cash side of a trade as a separate row, so the two legs are
 * paired here rather than a cash leg being derived, which would post the money
 * twice. Both rules are measured against the sample exports in local/masters: sells
 * pair 91/91 -- 69 of a security and 22 of cash -- and buys 78/78.
 *
 * Which cash row a trade pairs with is cashLegType's to say.
 *
 * Charges (dealing fee, PTM levy, stamp duty, FX charge) are never grouped with a
 * trade. Fidelity dates them on the order date while the trade settles later, so
 * folding them in would misdate them, and their money is already accounted for by
 * their own rows.
 */
export function assignFidelityGroups(legs: FidelityLeg[]): FidelityGroups {
  const refs = legs.map(() => "");
  const cashLegs = legs.map(() => false);
  // NUL separator, written as an escape so this file stays plain text. A Fidelity
  // date is "10 Feb 2022", so a separator a date could contain risks two different
  // (account, date) pairs collapsing onto one key.
  const bucket = (l: FidelityLeg) => `${l.account}\u0000${l.dateKey}`;
  const byType = new Map<string, number[]>();
  legs.forEach((l, i) => {
    if (!Number.isFinite(l.ref)) return;
    const seen = byType.get(l.type);
    if (seen) seen.push(i);
    else byType.set(l.type, [i]);
  });
  const indices = (type: string) => byType.get(type) ?? [];
  // Security trades pick their cash row first. Nothing in the sample exports needs
  // it -- no bucket holds a cash and a security trade of equal amount -- but five
  // hold both kinds, so this keeps which one wins from depending on the order the
  // broker happened to export them in.
  const tradeRows = (types: string[]) => {
    const idx = types.flatMap(indices);
    return [...idx.filter((i) => !legs[i].cashAsset), ...idx.filter((i) => legs[i].cashAsset)];
  };

  const pair = (securityIdx: number, cashIdx: number) => {
    const ref = String(legs[securityIdx].ref);
    refs[securityIdx] = ref;
    refs[cashIdx] = ref;
    cashLegs[cashIdx] = true;
  };

  // Both rules below match one broker-supplied total against another, so neither
  // notices a cash row that is internally consistent but belongs to a different
  // trade. quantity * unit price is derived from different fields, which makes it an
  // independent check: a swapped cash row misses it by percentage points.
  // A ratio test, so it divides: past the exactness boundary by construction,
  // and a band this wide would not notice the difference anyway. See
  // adr/0026-exact-decimals-bounded-by-closure.md.
  const consistent = (security: FidelityLeg, cash: FidelityLeg) => {
    const expected = Math.abs(security.consideration);
    // Nothing quoted to check against. Absence is not evidence of a bad pairing.
    if (expected === 0) return true;
    return Math.abs(Math.abs(cash.amount) - expected) / expected <= CONSIDERATION_BAND;
  };

  // Sells: the cash in equals the proceeds exactly, so the amount identifies it.
  const usedCash = new Set<number>();
  for (const s of tradeRows(SELL_ROWS)) {
    const match = indices(cashLegType(legs[s])).find(
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
  const candidates: Array<{ distance: number; buy: number; cash: number }> = [];
  for (const b of tradeRows(BUY_ROWS)) {
    for (const c of indices(cashLegType(legs[b]))) {
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

  // Deposits, last: a run is only ever built from cash rows the trade passes did
  // not want, which is what stops a deposit taking the cash row of a trade of the
  // same amount on the same day.
  //
  // Identified by account, amount and reference proximity rather than by
  // bucket(). One run in the sample exports settles across two days -- the lump
  // sum and its spend complete on the 11th, the arrival on the 14th -- so the
  // date the trade passes group on would split it, while the reference run holds
  // in all 21.
  for (const s of indices("Cash In Lump Sum")) {
    const members: number[] = [];
    for (const type of DEPOSIT_ROWS) {
      let best: number | undefined;
      for (const c of indices(type)) {
        if (usedCash.has(c)) continue;
        if (legs[c].account !== legs[s].account) continue;
        const gap = legs[c].ref - legs[s].ref;
        if (gap <= 0 || gap > DEPOSIT_REF_SPAN) continue;
        if (Math.abs(Math.abs(legs[c].amount) - Math.abs(legs[s].amount)) >= AMOUNT_EPSILON) {
          continue;
        }
        if (best === undefined || legs[c].ref < legs[best].ref) best = c;
      }
      if (best !== undefined) {
        members.push(best);
        usedCash.add(best);
      }
    }
    // A lump sum with no run is money paid in from outside -- the sample has
    // three, all into a cash management account -- and stands on its own.
    if (members.length === 0) continue;
    // Only the group ref. None of these is a trade's cash leg, and the group
    // needs to keep its JRNLFUND legs: they are what routes its residual to
    // TRANSFER_CLEARING, where the departure it answers is also waiting.
    const ref = String(legs[s].ref);
    refs[s] = ref;
    for (const m of members) refs[m] = ref;
  }

  return { refs, cashLegs };
}

/**
 * Retypes a journal that turned out to be a trade's cash leg.
 *
 * `Cash In` is normally money arriving from outside the account, but a switch
 * settles its sale through the same row type, and no lookup on the broker's own
 * name can tell the two apart. Whether the row paired with a trade can: a leg
 * that did settles inside the account, which is what CASHFLOW says. Left as
 * JRNLFUND it would make the trade group read as a transfer, so the group's
 * residual would be routed to TRANSFER_CLEARING rather than IMBALANCE.
 */
export function asTradeCashLeg(p: Posting, currency: string): void {
  if (p.type !== TxType.JRNLFUND) return;
  p.type = TxType.CASHFLOW;
  // isCashTxType covers CASHFLOW but not JRNLFUND, so the currency the row was
  // denied as a journal is owed to it as a cash leg.
  if (currency) p.tradingCurrency = currency;
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
  const legs: FidelityLeg[] = [];
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
    const mappedType = txTypeStr ? FIDELITY_TYPE_TO_OFX[txTypeStr] : undefined;
    if (mappedType === undefined) {
      errors.push({ rowIndex, field: "type", message: txTypeStr ? `Unknown transaction type: ${txTypeStr}` : "Missing transaction type" });
      continue;
    }

    const investments = get(investmentsCol);
    const assetClass = assetClassCol >= 0 ? get(assetClassCol) : "";
    const cashAsset = assetClass ? assetClass === "CASH" : investments === "Cash";
    const ofxType = typeForAsset(mappedType, cashAsset);

    // A cash posting is described by its currency, which is what resolves it to
    // the currency instrument rather than to a holding named after the broker's
    // wording. The export says "Cash" in the same column it names a security in,
    // so reading it through produced a security called Cash. See
    // docs/spec/archive-format.md.
    const instrumentDescription = isCashMovement(ofxType)
      ? currency
      : investments || txTypeStr;
    const account = accountCol >= 0 ? get(accountCol) : "";
    const qtyStr = get(qtyCol);
    const amountStr = amountCol >= 0 ? get(amountCol) : "";
    const amountDec = parseDecimal(amountStr);

    const rawQtyDec = parseDecimal(qtyStr);
    let quantity = rawQtyDec ?? ZERO;
    if (isCashMovement(ofxType) && amountDec !== undefined) {
      // Quantity is an unsigned magnitude: a fee and the interest that paid for
      // it both report a positive number, and the direction survives only in the
      // sign of Amount. Quantity is also 0 on some rows where money did move
      // (Tax On Interest reports 0 against an Amount of -0.20), so for cash the
      // transacted value is Amount itself rather than a sign-corrected Quantity.
      quantity = amountDec;
    } else if (SELL_TYPES.has(ofxType)) {
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
    const identifierHints = isCashMovement(ofxType)
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

    // dateStr, not the parsed date: both legs of a trade carry the same string,
    // and pairing only needs them to agree.
    // The raw quantity, not the sign-corrected one: cash rows overwrite it with
    // their amount, which would make the check compare a total against itself.
    legs.push({
      type: txTypeStr,
      account,
      dateKey: dateStr,
      amount: amountDec?.toNumber() ?? 0,
      // The product is exact; the band it is compared against is not, which is
      // why the leg carries it as a number.
      consideration:
        rawQtyDec !== undefined && unitPriceDec !== undefined
          ? rawQtyDec.times(unitPriceDec).abs().toNumber()
          : 0,
      ref: refCol >= 0 ? parseInt(get(refCol), 10) : NaN,
      cashAsset,
    });
    // The cell, not the parsed number the leg carries: broker_ref is opaque and a
    // reference the source wrote in some other shape should survive rather than
    // become NaN. The correlation beside it carries the number, in a field of its
    // own, because knowing how to take a number out of this broker's references
    // is exactly what a converter uniquely knows.
    //
    // No counterpartyAccount. The export's "Source investment" column holds an
    // asset name, not an account, so this file names no counterparty anywhere.
    // Only the JSON the extension reads does.
    const brokerRef = refCol >= 0 ? get(refCol) : "";
    const correlation = fidelityRefCorrelation(brokerRef);
    postings.push(
      create(PostingSchema, {
        timestamp: timestampFromDate(date),
        instrumentDescription,
        type: ofxType,
        quantity: quantity.toString(),
        account,
        settlementCurrency: currency,
        ...(isCashTxType(ofxType) ? { tradingCurrency: currency } : {}),
        // Presence, not truthiness: a reported price of zero is a price.
        ...(unitPriceDec !== undefined ? { unitPrice: unitPriceDec.toString() } : {}),
        ...(brokerRef ? { brokerRef } : {}),
        ...(correlation ? { correlations: [correlation] } : {}),
        ...(identifierHints.length > 0 ? { identifierHints } : {}),
      })
    );
  }

  const groups = assignFidelityGroups(legs);
  groups.refs.forEach((ref, i) => {
    if (ref) postings[i].groupRef = ref;
  });
  groups.cashLegs.forEach((paired, i) => {
    if (paired) asTradeCashLeg(postings[i], currency);
  });
  // After the refs are stamped, since that loop is index-parallel with legs.
  // Fidelity nets nothing into a trade total, so no fee is derived here: its
  // charges arrive as their own rows and only need the account they went to.
  postings.push(...counterLegs(postings));

  const periodFrom = minTime === Infinity ? new Date(0) : new Date(minTime);
  const periodBefore =
    maxTime === -Infinity ? new Date(0) : startOfNextDay(new Date(maxTime));

  return { postings, periodFrom, periodBefore, errors };
}
