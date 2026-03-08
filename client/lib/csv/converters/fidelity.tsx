/**
 * Fidelity CSV to standard format converter.
 * Requires user to specify currency via FidelityOptions component.
 */

import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Broker } from "@/gen/api/v1/api_pb";
import { TxSchema, TxType } from "@/gen/api/v1/api_pb";
import type { StandardParseResult, ParseError } from "@/lib/csv/standard";
import { register } from "./registry";
import type { ConverterOptionsProps } from "./registry";

const FIDELITY_DATE_FORMAT = /^(\d{1,2})\s+([A-Za-z]{3})\s+(\d{4})$/;
const MONTHS: Record<string, number> = {
  Jan: 0, Feb: 1, Mar: 2, Apr: 3, May: 4, Jun: 5,
  Jul: 6, Aug: 7, Sep: 8, Oct: 9, Nov: 10, Dec: 11,
};

function parseCSVLine(line: string): string[] {
  const result: string[] = [];
  let current = "";
  let inQuotes = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (inQuotes) {
      if (c === '"') inQuotes = false;
      else current += c;
    } else {
      if (c === '"') inQuotes = true;
      else if (c === ",") {
        result.push(current);
        current = "";
      } else current += c;
    }
  }
  result.push(current);
  return result;
}

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

const FIDELITY_TYPE_TO_OFX: Record<string, TxType> = {
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
  "Cash Out For Buy": TxType.JRNLFUND,
};

function isCashTxType(type: TxType): boolean {
  return (
    type === TxType.INCOME ||
    type === TxType.INVEXPENSE ||
    type === TxType.REINVEST ||
    type === TxType.TRANSFER ||
    type === TxType.MARGININTEREST ||
    type === TxType.RETOFCAP
  );
}

export function convertFidelityToStandard(
  csvText: string,
  options: { currency: string }
): StandardParseResult {
  const errors: ParseError[] = [];
  const currency = options?.currency ?? "";
  if (!currency) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodTo: new Date(0),
      errors: [{ rowIndex: 0, field: "options", message: "Currency is required" }],
    };
  }

  const lines = csvText.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.length > 0);
  if (lines.length === 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodTo: new Date(0),
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
      periodTo: new Date(0),
      errors: [{ rowIndex: 0, field: "file", message: "Could not find Fidelity data header (Order date)" }],
    };
  }

  const headerLower = headerRow.map((h) => h.trim().toLowerCase().replace(/\s+/g, "_"));
  const col = (name: string): number => {
    const n = name.toLowerCase().replace(/\s+/g, "_");
    return headerLower.indexOf(n);
  };
  const orderDateCol = col("order_date");
  const txTypeCol = col("transaction_type");
  const investmentsCol = col("investments");
  const accountCol = col("account_number");
  const qtyCol = col("quantity");
  const priceCol = col("price_per_unit");

  if (orderDateCol < 0 || txTypeCol < 0 || investmentsCol < 0) {
    return {
      txs: [],
      periodFrom: new Date(0),
      periodTo: new Date(0),
      errors: [{ rowIndex: headerRowIndex + 1, field: "header", message: "Missing required Fidelity columns" }],
    };
  }

  const txs: ReturnType<typeof create>[] = [];
  let minTime = Infinity;
  let maxTime = -Infinity;

  for (let i = headerRowIndex + 1; i < lines.length; i++) {
    const rowIndex = i + 1;
    const values = parseCSVLine(lines[i]);
    const get = (idx: number) => (idx >= 0 && idx < values.length ? values[idx].trim() : "");

    const orderDateStr = get(orderDateCol);
    const date = parseFidelityDate(orderDateStr);
    if (!date) {
      errors.push({ rowIndex, field: "date", message: "Invalid or missing order date" });
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
    let quantity = parseFloat(qtyStr);
    if (Number.isNaN(quantity)) quantity = 0;
    if (ofxType === TxType.SELLSTOCK || ofxType === TxType.SELLMF || ofxType === TxType.SELLDEBT || ofxType === TxType.SELLOPT || ofxType === TxType.SELLOTHER) {
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
  const periodTo = maxTime === -Infinity ? new Date(0) : new Date(maxTime);

  return { txs, periodFrom, periodTo, errors };
}

const CURRENCIES = ["GBP", "USD", "EUR", "CHF", "JPY"];

export function FidelityOptions({ onOptionsChange, options }: ConverterOptionsProps) {
  return (
    <div className="space-y-2">
      <label htmlFor="fidelity-currency" className="block text-sm font-medium text-text-primary">
        Currency
      </label>
      <select
        id="fidelity-currency"
        value={(options?.currency as string) ?? ""}
        onChange={(e) => onOptionsChange({ currency: e.target.value || undefined })}
        className="block w-full rounded-lg border border-border bg-surface px-3 py-2 text-text-primary"
      >
        <option value="">Select currency</option>
        {CURRENCIES.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
    </div>
  );
}

register({
  broker: Broker.FIDELITY,
  label: "Fidelity",
  sourcePrefix: "Fidelity",
  formats: [
    {
      id: "fidelity-csv",
      label: "Fidelity CSV",
      convert: convertFidelityToStandard,
      OptionsComponent: FidelityOptions,
    },
  ],
});
