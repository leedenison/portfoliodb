#!/usr/bin/env node
/**
 * Converts IBKR activity statement CSV to the standard transaction CSV format.
 * - INITIALISE (stock) -> BUYSTOCK or SELLSTOCK; description = full IBKR description (from later rows when available).
 * - CASH (and cash-like) -> JRNLFUND; description = currency symbol (USD, GBP, etc).
 * - Other types mapped to standard (BUYSTOCK, SELLSTOCK, INCOME, SPLIT, etc.).
 */

import fs from "fs";
import path from "path";

const IBKR_PATH = path.join(process.cwd(), "local", "IBKR.csv");
const OUT_PATH = path.join(process.cwd(), "local", "IBKR-standard.csv");

function parseCSVLine(line) {
  const result = [];
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

function parseNum(s) {
  if (s == null || s === "") return NaN;
  const cleaned = String(s).trim().replace(/,/g, "");
  return parseFloat(cleaned);
}

// Standard types we emit
const STD = {
  BUYSTOCK: "BUYSTOCK",
  SELLSTOCK: "SELLSTOCK",
  BUYOPT: "BUYOPT",
  SELLOPT: "SELLOPT",
  INCOME: "INCOME",
  SPLIT: "SPLIT",
  JRNLFUND: "JRNLFUND",
  INVEXPENSE: "INVEXPENSE",
};

// IBKR column indices
const C = {
  Date: 0,
  Account: 1,
  Type: 2,
  Subtype: 3,
  Description: 4,
  Ticker: 5,
  Units: 6,
  Price: 7,
  Amount: 8,
  Currency: 9,
  Exchange: 10,
  Symbol: 11,
  Option: 12,
  SecType: 13, // second "Type" column: STOCK, CASH, FX, OPTION
  UnderlyingType: 15,
};

function get(row, i) {
  return (row[i] ?? "").trim();
}

function main() {
  const raw = fs.readFileSync(IBKR_PATH, "utf8");
  const lines = raw.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  if (lines.length < 2) {
    console.error("IBKR.csv has no data rows");
    process.exit(1);
  }

  const rows = lines.slice(1).map((line) => parseCSVLine(line));

  // First pass: build symbol -> full IBKR stock description (e.g. "ABNB AIRBNB INC-CLASS A"); IBKR puts it in Description or Ticker
  const symbolToFullDesc = new Map();
  for (const row of rows) {
    const symbol = get(row, C.Symbol);
    const desc = get(row, C.Description) || get(row, C.Ticker);
    if (!symbol || !desc || desc.length <= symbol.length) continue;
    if (!desc.toUpperCase().startsWith(symbol.toUpperCase())) continue;
    // Exclude dividend/split/option text - only keep stock-name style descriptions
    if (desc.includes("(US") || desc.includes("DIVIDEND") || desc.includes(" SPLIT ") || desc.includes("PER SHARE")) continue;
    if (!symbolToFullDesc.has(symbol)) symbolToFullDesc.set(symbol, desc);
  }

  const outRows = [["date", "instrument_description", "type", "quantity", "currency", "unit_price"]];

  for (const row of rows) {
    const date = get(row, C.Date);
    const type = get(row, C.Type);
    const subtype = get(row, C.Subtype);
    const description = get(row, C.Description);
    const ticker = get(row, C.Ticker);
    const symbol = get(row, C.Symbol);
    const unitsStr = get(row, C.Units);
    const priceStr = get(row, C.Price);
    const amountStr = get(row, C.Amount);
    const currency = get(row, C.Currency);
    const units = parseNum(unitsStr);
    const amount = parseNum(amountStr);
    const price = parseNum(priceStr);

    const descForInstrument = description || ticker || symbol;
    const fullDesc = symbolToFullDesc.get(symbol) || symbolToFullDesc.get(ticker) || description || ticker || symbol;

    let stdType = null;
    let qty = null;
    let instrumentDescription = null;

    // CASH (any subtype including INITIALISE) -> JRNLFUND, description = currency
    if (type === "CASH") {
      const q = parseNum(amountStr);
      if (Number.isNaN(q) || !currency) continue;
      stdType = STD.JRNLFUND;
      qty = q;
      instrumentDescription = currency;
    }
    // INITIALISE stock -> BUYSTOCK or SELLSTOCK; use full IBKR description
    else if ((type === "INITIALISE" || subtype === "INITIALISE") && get(row, C.SecType) === "STOCK") {
      const q = parseNum(unitsStr);
      if (Number.isNaN(q)) continue;
      stdType = q >= 0 ? STD.BUYSTOCK : STD.SELLSTOCK;
      qty = q;
      instrumentDescription = fullDesc || symbol || ticker;
    }
    // Broker Interest, Deposits/Withdrawals, Withholding Tax, etc. -> JRNLFUND with description = currency
    else if (
      type === "Broker Interest Received" ||
      type === "Broker Interest Paid" ||
      type === "Deposits/Withdrawals" ||
      type === "Withholding Tax" ||
      type === "Payment In Lieu Of Dividends"
    ) {
      const q = parseNum(amountStr);
      if (Number.isNaN(q) || !currency) continue;
      stdType = type === "Withholding Tax" || type === "Broker Interest Paid" ? STD.INVEXPENSE : STD.JRNLFUND;
      qty = q;
      instrumentDescription = currency;
    }
    // BUY / SELL
    else if (type === "BUY" || type === "SELL") {
      const sub = subtype.toUpperCase();
      if (sub === "BUYSTOCK" || sub === "SELLSTOCK") {
        stdType = sub === "BUYSTOCK" ? STD.BUYSTOCK : STD.SELLSTOCK;
        qty = units;
        instrumentDescription = fullDesc || descForInstrument;
      } else if (sub === "BUYOPT" || sub === "SELLOPT") {
        stdType = sub === "BUYOPT" ? STD.BUYOPT : STD.SELLOPT;
        qty = units;
        instrumentDescription = fullDesc || descForInstrument;
      }
    }
    // INCOME (e.g. DIV)
    else if (type === "INCOME") {
      const q = parseNum(amountStr);
      if (Number.isNaN(q)) continue;
      stdType = STD.INCOME;
      qty = q;
      instrumentDescription = fullDesc || descForInstrument;
    }
    // STOCKSPLIT
    else if (type === "STOCKSPLIT") {
      const q = parseNum(unitsStr);
      if (Number.isNaN(q)) continue;
      stdType = STD.SPLIT;
      qty = q;
      instrumentDescription = fullDesc || descForInstrument;
    }

    if (stdType == null || qty == null || instrumentDescription == null) continue;
    if (!date) continue;

    const unitPrice = priceStr !== "" && !Number.isNaN(price) ? String(price) : "";
    const currencyCell = currency || "";
    outRows.push([date, instrumentDescription, stdType, String(qty), currencyCell, unitPrice]);
  }

  const csv = outRows.map((r) => r.map((c) => (c.includes(",") || c.includes('"') ? `"${c.replace(/"/g, '""')}"` : c)).join(",")).join("\n");
  fs.writeFileSync(OUT_PATH, csv + "\n", "utf8");
  console.log(`Wrote ${outRows.length - 1} rows to ${OUT_PATH}`);
}

main();
