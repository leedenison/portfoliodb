import { describe, expect, it } from "vitest";
import { parseStandardCSV } from "./standard";
import { IdentifierType, TxType } from "@/gen/api/v1/api_pb";

describe("parseStandardCSV", () => {
  it("parses valid CSV and derives period from min/max dates", () => {
    const csv = `date,instrument_description,type,quantity,trading_currency,settlement_currency,unit_price
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,USD,185.50
2024-01-10,MSFT - Microsoft,SELLSTOCK,-5,USD,USD,398.20`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(2);
    expect(result.txs[0].instrumentDescription).toBe("AAPL - Apple Inc.");
    expect(result.txs[0].type).toBe(TxType.BUYSTOCK);
    expect(result.txs[0].quantity).toBe(10);
    expect(result.txs[0].tradingCurrency).toBe("USD");
    expect(result.txs[0].settlementCurrency).toBe("USD");
    expect(result.txs[0].unitPrice).toBe(185.5);

    expect(result.txs[1].instrumentDescription).toBe("MSFT - Microsoft");
    expect(result.txs[1].type).toBe(TxType.SELLSTOCK);
    expect(result.txs[1].quantity).toBe(-5);

    expect(result.periodFrom.getTime()).toBe(new Date("2024-01-10").getTime());
    expect(result.periodTo.getTime()).toBe(new Date("2024-01-15").getTime());
  });

  it("accepts timestamp column instead of date", () => {
    const csv = `timestamp,instrument_description,type,quantity
2024-02-01T12:00:00Z,SYMBOL,BUYSTOCK,1`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].timestamp).toBeDefined();
  });

  it("accepts case-insensitive headers", () => {
    const csv = `DATE,Instrument_Description,TYPE,Quantity
2024-01-01,FOO,BUYSTOCK,1`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].instrumentDescription).toBe("FOO");
  });

  it("returns error for empty file", () => {
    const result = parseStandardCSV("");

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("file");
    expect(result.errors[0].message).toContain("empty");
  });

  it("returns error when required columns are missing", () => {
    const csv = `date,instrument_description
2024-01-01,AAPL`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors.some((e) => e.field === "header" && e.message.includes("type"))).toBe(true);
    expect(result.errors.some((e) => e.field === "header" && e.message.includes("quantity"))).toBe(true);
  });

  it("returns error for invalid date", () => {
    const csv = `date,instrument_description,type,quantity
not-a-date,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("date");
    expect(result.errors[0].message).toContain("Invalid");
  });

  it("returns error for unknown transaction type", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,INVALIDTYPE,10`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("type");
    expect(result.errors[0].message).toContain("Unknown");
  });

  it("returns error for non-numeric quantity", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,ten`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("quantity");
    expect(result.errors[0].message).toContain("number");
  });

  it("returns error for missing instrument_description", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("instrument_description");
  });

  it("handles quoted fields with commas", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,"Apple, Inc. - AAPL",BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].instrumentDescription).toBe("Apple, Inc. - AAPL");
  });

  it("allows optional trading_currency, settlement_currency and unit_price to be omitted", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].quantity).toBe(10);
    expect(result.txs[0].instrumentDescription).toBe("AAPL");
    // Optional fields may be unset or proto default ("" or 0)
    expect([undefined, ""]).toContain(result.txs[0].settlementCurrency);
    expect([undefined, ""]).toContain(result.txs[0].tradingCurrency);
  });

  it("returns error for invalid unit_price when present", () => {
    const csv = `date,instrument_description,type,quantity,unit_price
2024-01-01,AAPL,BUYSTOCK,10,not-a-number`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("unit_price");
  });

  it("skips invalid rows but parses valid ones and reports errors", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10
not-a-date,FOO,BUYSTOCK,5
2024-01-03,MSFT,SELLSTOCK,-3`;

    const result = parseStandardCSV(csv);

    expect(result.txs).toHaveLength(2);
    expect(result.txs[0].instrumentDescription).toBe("AAPL");
    expect(result.txs[1].instrumentDescription).toBe("MSFT");
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].rowIndex).toBe(3);
    expect(result.errors[0].field).toBe("date");
    expect(result.periodFrom.getTime()).toBe(new Date("2024-01-01").getTime());
    expect(result.periodTo.getTime()).toBe(new Date("2024-01-03").getTime());
  });

  it("parses account, trading_currency, settlement_currency", () => {
    const csv = `date,instrument_description,type,quantity,account,trading_currency,settlement_currency
2024-01-01,AAPL,BUYSTOCK,10,ACC-123,EUR,GBP`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].account).toBe("ACC-123");
    expect(result.txs[0].tradingCurrency).toBe("EUR");
    expect(result.txs[0].settlementCurrency).toBe("GBP");
  });

  it("parses exchange_code_hint and mic_hint", () => {
    const csv = `date,instrument_description,type,quantity,exchange_code_hint,mic_hint
2024-01-01,AAPL,BUYSTOCK,10,US,XNAS`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].exchangeCodeHint).toBe("US");
    expect(result.txs[0].micHint).toBe("XNAS");
  });

  it("accepts exchange and mic as aliases for exchange_code_hint and mic_hint", () => {
    const csv = `date,instrument_description,type,quantity,exchange,mic
2024-01-01,AAPL,BUYSTOCK,10,LON,XLON`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].exchangeCodeHint).toBe("LON");
    expect(result.txs[0].micHint).toBe("XLON");
  });

  it("parses identifier hint columns isin, ticker, ticker_exchange, openfigi_share_class, occ", () => {
    const csv = `date,instrument_description,type,quantity,isin,ticker,ticker_exchange,openfigi_share_class,occ
2024-01-01,AAPL,BUYSTOCK,10,US0378331005,AAPL,US,BG0000000000,
2024-01-02,MSFT Option,BUYOPT,1,,,US,,OCC-123`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(2);

    // First row: isin, ticker (with ticker_exchange), openfigi_share_class = 3 hints (occ is empty)
    expect(result.txs[0].identifierHints).toHaveLength(3);
    expect(result.txs[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.ISIN, value: "US0378331005", canonical: false })
    );
    expect(result.txs[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.TICKER, value: "AAPL", domain: "US", canonical: false })
    );
    expect(result.txs[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.OPENFIGI_SHARE_CLASS, value: "BG0000000000", canonical: false })
    );

    expect(result.txs[1].identifierHints).toHaveLength(1);
    expect(result.txs[1].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.OCC, value: "OCC-123", canonical: false })
    );
  });

  it("accepts ticker_domain as alias for ticker_exchange", () => {
    const csv = `date,instrument_description,type,quantity,ticker,ticker_domain
2024-01-01,FOO,BUYSTOCK,1,SYM,LON`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.TICKER, value: "SYM", domain: "LON", canonical: false })
    );
  });
});
