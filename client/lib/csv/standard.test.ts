import { describe, expect, it } from "vitest";
import { parseStandardCSV } from "./standard";
import { AccountType, IdentifierType, TxType } from "@/gen/type/v1/type_pb";

describe("parseStandardCSV", () => {
  it("parses valid CSV and derives period from min/max dates", () => {
    const csv = `date,instrument_description,type,quantity,trading_currency,settlement_currency,unit_price
2024-01-15,AAPL - Apple Inc.,BUYSTOCK,10,USD,USD,185.50
2024-01-10,MSFT - Microsoft,SELLSTOCK,-5,USD,USD,398.20`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0].instrumentDescription).toBe("AAPL - Apple Inc.");
    expect(result.postings[0].type).toBe(TxType.BUYSTOCK);
    expect(result.postings[0].quantity).toBe("10");
    expect(result.postings[0].tradingCurrency).toBe("USD");
    expect(result.postings[0].settlementCurrency).toBe("USD");
    expect(result.postings[0].unitPrice).toBe("185.5");

    expect(result.postings[1].instrumentDescription).toBe("MSFT - Microsoft");
    expect(result.postings[1].type).toBe(TxType.SELLSTOCK);
    expect(result.postings[1].quantity).toBe("-5");

    expect(result.periodFrom.getTime()).toBe(new Date("2024-01-10").getTime());
    // Exclusive: midnight after the last row's day, so the last row is inside.
    const lastRow = new Date("2024-01-15");
    expect(result.periodBefore.getTime()).toBeGreaterThan(lastRow.getTime());
    expect(result.periodBefore).toEqual(
      new Date(lastRow.getFullYear(), lastRow.getMonth(), lastRow.getDate() + 1)
    );
  });

  it("accepts timestamp column instead of date", () => {
    const csv = `timestamp,instrument_description,type,quantity
2024-02-01T12:00:00Z,SYMBOL,BUYSTOCK,1`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].timestamp).toBeDefined();
  });

  it("accepts case-insensitive headers", () => {
    const csv = `DATE,Instrument_Description,TYPE,Quantity
2024-01-01,FOO,BUYSTOCK,1`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].instrumentDescription).toBe("FOO");
  });

  it("returns error for empty file", () => {
    const result = parseStandardCSV("");

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("file");
    expect(result.errors[0].message).toContain("empty");
  });

  it("returns error when required columns are missing", () => {
    const csv = `date,instrument_description
2024-01-01,AAPL`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors.some((e) => e.field === "header" && e.message.includes("type"))).toBe(true);
    expect(result.errors.some((e) => e.field === "header" && e.message.includes("quantity"))).toBe(true);
  });

  it("returns error for invalid date", () => {
    const csv = `date,instrument_description,type,quantity
not-a-date,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("date");
    expect(result.errors[0].message).toContain("Invalid");
  });

  it("returns error for unknown transaction type", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,INVALIDTYPE,10`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("type");
    expect(result.errors[0].message).toContain("Unknown");
  });

  it("returns error for non-numeric quantity", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,ten`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("quantity");
    expect(result.errors[0].message).toContain("number");
  });

  it("returns error for missing instrument_description", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("instrument_description");
  });

  it("handles quoted fields with commas", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,"Apple, Inc. - AAPL",BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].instrumentDescription).toBe("Apple, Inc. - AAPL");
  });

  it("allows optional trading_currency, settlement_currency and unit_price to be omitted", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].quantity).toBe("10");
    expect(result.postings[0].instrumentDescription).toBe("AAPL");
    // Optional fields may be unset or proto default ("" or 0)
    expect([undefined, ""]).toContain(result.postings[0].settlementCurrency);
    expect([undefined, ""]).toContain(result.postings[0].tradingCurrency);
  });

  it("returns error for invalid unit_price when present", () => {
    const csv = `date,instrument_description,type,quantity,unit_price
2024-01-01,AAPL,BUYSTOCK,10,not-a-number`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("unit_price");
  });

  it("skips invalid rows but parses valid ones and reports errors", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10
not-a-date,FOO,BUYSTOCK,5
2024-01-03,MSFT,SELLSTOCK,-3`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[0].instrumentDescription).toBe("AAPL");
    expect(result.postings[1].instrumentDescription).toBe("MSFT");
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].rowIndex).toBe(3);
    expect(result.errors[0].field).toBe("date");
    expect(result.periodFrom.getTime()).toBe(new Date("2024-01-01").getTime());
    const lastRow = new Date("2024-01-03");
    expect(result.periodBefore.getTime()).toBeGreaterThan(lastRow.getTime());
  });

  it("parses account, trading_currency, settlement_currency", () => {
    const csv = `date,instrument_description,type,quantity,account,trading_currency,settlement_currency
2024-01-01,AAPL,BUYSTOCK,10,ACC-123,EUR,GBP`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].account).toBe("ACC-123");
    expect(result.postings[0].tradingCurrency).toBe("EUR");
    expect(result.postings[0].settlementCurrency).toBe("GBP");
  });

  it("parses symbol_type + symbol as identifier hint", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL,BUYSTOCK,10,MIC_TICKER,AAPL`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(1);
    expect(result.postings[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.MIC_TICKER, value: "AAPL" })
    );
  });

  it("parses symbol_type + symbol + exchange_type + exchange with MIC domain", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol,exchange_type,exchange
2024-01-01,AAPL,BUYSTOCK,10,MIC_TICKER,AAPL,MIC,XNAS`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(1);
    expect(result.postings[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.MIC_TICKER, value: "AAPL", domain: "XNAS" })
    );
  });

  it("parses OPENFIGI_TICKER with OPENFIGI exchange domain", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol,exchange_type,exchange
2024-01-01,AAPL,BUYSTOCK,10,OPENFIGI_TICKER,AAPL,OPENFIGI,US`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(1);
    expect(result.postings[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.OPENFIGI_TICKER, value: "AAPL", domain: "US" })
    );
  });

  it("parses ISIN symbol_type without exchange", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL,BUYSTOCK,10,ISIN,US0378331005`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(1);
    expect(result.postings[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.ISIN, value: "US0378331005" })
    );
  });

  it("parses OCC symbol_type", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL Option,BUYOPT,1,OCC,AAPL  240119C00185000`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(1);
    expect(result.postings[0].identifierHints).toContainEqual(
      expect.objectContaining({ type: IdentifierType.OCC, value: "AAPL  240119C00185000" })
    );
  });

  it("returns error for unknown symbol_type", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL,BUYSTOCK,10,BOGUS,AAPL`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("symbol_type");
    expect(result.errors[0].message).toContain("Unknown");
  });

  it("returns error for unknown exchange_type", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol,exchange_type,exchange
2024-01-01,AAPL,BUYSTOCK,10,MIC_TICKER,AAPL,BOGUS,XNAS`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("exchange_type");
    expect(result.errors[0].message).toContain("Unknown");
  });

  it("returns error when symbol_type present but symbol empty", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL,BUYSTOCK,10,MIC_TICKER,`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("symbol");
  });

  it("returns error when exchange_type present but exchange empty", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol,exchange_type,exchange
2024-01-01,AAPL,BUYSTOCK,10,MIC_TICKER,AAPL,MIC,`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("exchange");
  });

  it("produces no hint when symbol columns are absent", () => {
    const csv = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(0);
  });

  it("produces no hint when symbol_type and symbol are both empty", () => {
    const csv = `date,instrument_description,type,quantity,symbol_type,symbol
2024-01-01,AAPL,BUYSTOCK,10,,`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].identifierHints).toHaveLength(0);
  });

describe("share count basis", () => {
  const rows = `date,instrument_description,type,quantity
2024-01-01,AAPL,BUYSTOCK,10`;

  it("is undeclared for an ordinary as-traded file", () => {
    const result = parseStandardCSV(rows);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].shareCountBasis).toBeUndefined();
  });

  // The comment declares one basis for the whole file; the archive states it per
  // posting, so every posting carries the declared value.
  it("reads the basis from a comment line onto every posting", () => {
    const result = parseStandardCSV(`# share_count_basis=2026-07-29\n${rows}`);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].shareCountBasis).toBe("2026-07-29");
  });

  it("rejects a basis that is not a plain date", () => {
    const result = parseStandardCSV(`# share_count_basis=29/07/2026\n${rows}`);

    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("share_count_basis");
  });

  it("ignores other comment lines rather than parsing them as rows", () => {
    const result = parseStandardCSV(`# exported from somewhere\n${rows}`);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
  });
});
});

describe("group_ref", () => {
  it("carries the grouping key onto each posting", () => {
    const csv = `date,instrument_description,type,quantity,unit_price,account,group_ref
2024-03-01,VOD - Vodafone,SELLSTOCK,-100,1.25,ACC1,441416452
2024-03-01,Cash,CASHFLOW,125,1,ACC1,441416452
2024-03-01,Dealing Fee,INVEXPENSE,-7.5,1,ACC1,`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(3);
    expect(result.postings[0].groupRef).toBe("441416452");
    expect(result.postings[1].groupRef).toBe("441416452");
    // A separately-reported charge is its own event, so it names no group.
    expect(result.postings[2].groupRef).toBeUndefined();
  });

  it("defaults to empty when the column is absent", () => {
    const csv = `date,instrument_description,type,quantity
2024-03-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].groupRef).toBeUndefined();
  });
});

describe("source references", () => {
  it("carries the broker's own reference and the account it names", () => {
    const csv = `date,instrument_description,type,quantity,account,group_ref,broker_ref,counterparty_account
2025-04-15,GBP,TRANSFER,-20000,AG10000001,,971613411,
2025-04-15,GBP,TRANSFER,20000,AW10000001,,971613414,AG10000001`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0].brokerRef).toBe("971613411");
    // Only the receiving side names the counterparty; the departure does not.
    expect(result.postings[0].counterpartyAccount).toBeUndefined();
    expect(result.postings[1].brokerRef).toBe("971613414");
    expect(result.postings[1].counterpartyAccount).toBe("AG10000001");
  });

  it("is opaque: a reference that is not a number survives verbatim", () => {
    const csv = `date,instrument_description,type,quantity,broker_ref
2024-03-01,AAPL,BUYSTOCK,10,20240301U1234567`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].brokerRef).toBe("20240301U1234567");
  });

  it("defaults to empty when the columns are absent", () => {
    const csv = `date,instrument_description,type,quantity
2024-03-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].brokerRef).toBeUndefined();
    expect(result.postings[0].counterpartyAccount).toBeUndefined();
  });
});

describe("account_type", () => {
  it("types the non-asset leg of a group", () => {
    const csv = `date,instrument_description,type,quantity,account,group_ref,account_type
2024-02-01,USD,INCOME,23.40,ACC1,div-8842,
2024-02-01,USD,INCOME,-23.40,ACC1,div-8842,INCOME`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(2);
    // The cash arriving in the account is an ordinary posting; the income it came
    // from is the other side of the same event.
    expect(result.postings[0].accountType).toBe(AccountType.USER);
    expect(result.postings[1].accountType).toBe(AccountType.INCOME);
    expect(result.postings[1].groupRef).toBe("div-8842");
  });

  it("is case-insensitive and accepts the whole vocabulary", () => {
    const csv = `date,instrument_description,type,quantity,account_type
2024-02-01,X,BUYSTOCK,1,equity
2024-02-01,X,BUYSTOCK,1,Expense
2024-02-01,X,BUYSTOCK,1,IMBALANCE
2024-02-01,X,BUYSTOCK,1,transfer_clearing`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings.map((t) => t.accountType)).toEqual([
      AccountType.EQUITY,
      AccountType.EXPENSE,
      AccountType.IMBALANCE,
      AccountType.TRANSFER_CLEARING,
    ]);
  });

  it("defaults to USER when the column is absent", () => {
    const csv = `date,instrument_description,type,quantity
2024-03-01,AAPL,BUYSTOCK,10`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].accountType).toBe(AccountType.USER);
  });

  it("rejects an unknown value rather than falling back to USER", () => {
    const csv = `date,instrument_description,type,quantity,account_type
2024-03-01,AAPL,BUYSTOCK,10,Imbalance.USD`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("account_type");
  });

  it("does not accept UNSPECIFIED as a written value", () => {
    const csv = `date,instrument_description,type,quantity,account_type
2024-03-01,AAPL,BUYSTOCK,10,UNSPECIFIED`;

    const result = parseStandardCSV(csv);

    expect(result.postings).toHaveLength(0);
    expect(result.errors[0].field).toBe("account_type");
  });
});

describe("unit_price", () => {
  it("keeps a declared zero distinct from an empty cell", () => {
    // An option expiring worthless is priced at zero and its group balances; a
    // row whose converter supplied no price cannot be converted at all.
    const csv = `date,instrument_description,type,quantity,unit_price
2024-02-01,OPT,CLOSUREOPT,-1,0
2024-02-01,OPT,BUYOPT,1,`;

    const result = parseStandardCSV(csv);

    expect(result.errors).toHaveLength(0);
    expect(result.postings[0].unitPrice).toBe("0");
    expect(result.postings[1].unitPrice).toBeUndefined();
  });
});
