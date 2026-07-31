/**
 * Row shapes are taken from a real Fidelity export; account numbers and
 * quantities are altered, identifiers and the field structure are not.
 */

import { describe, expect, it } from "vitest";
import { IdentifierType, TxType } from "@/gen/api/v1/api_pb";
import { convertFidelityJson, isValidIsin } from "./fidelity-json";

const BUY = {
  accountNumber: "ACC-1",
  transactionType: "Buy",
  assetName: "VANGUARD FUNDS PLC, S&P 500 UCITS ETF USD DIS (VUSA)",
  isin: "IE00B3XXRP09",
  sedol: "B7NLLS3",
  units: 913,
  valuation: 72909.39,
  pricePerUnit: 79.848724,
  currency: "GBP",
  status: "Completed",
  debitCreditIndicator: "CREDIT",
  dealDate: "02/06/2026",
  settlementDate: "04/06/2026",
};

const SERVICE_FEE = {
  accountNumber: "ACC-2",
  transactionType: "Service Fee",
  assetName: "Cash",
  isin: "AA00S0000000",
  sedol: "S000000",
  units: 5.2,
  valuation: 5.2,
  pricePerUnit: 1,
  currency: "GBP",
  status: "Completed",
  debitCreditIndicator: "DEBIT",
  dealDate: "03/07/2026",
  settlementDate: "08/07/2026",
};

const json = (...rows: unknown[]) => JSON.stringify(rows);

describe("isValidIsin", () => {
  it("accepts genuine identifiers", () => {
    for (const isin of ["IE00B3XXRP09", "GB0002634946", "DE0007030009", "FR0000073272"]) {
      expect(isValidIsin(isin)).toBe(true);
    }
  });

  it("rejects the cash pseudo-identifiers Fidelity emits", () => {
    for (const isin of ["AA00K0000000", "AA00R0000000", "AA00S0000000"]) {
      expect(isValidIsin(isin)).toBe(false);
    }
  });

  it("rejects malformed values", () => {
    for (const isin of ["", "IE00B3XXRP0", "IE00B3XXRP090", "ie00b3xxrp09", "IE00B3XXRP08"]) {
      expect(isValidIsin(isin)).toBe(false);
    }
  });
});

describe("convertFidelityJson", () => {
  it("maps a purchase to its share count with both identifiers", () => {
    const result = convertFidelityJson(json(BUY));
    expect(result.errors).toEqual([]);
    const tx = result.txs[0]!;
    expect(tx.type).toBe(TxType.BUYSTOCK);
    expect(tx.quantity).toBe(913);
    expect(tx.unitPrice).toBeCloseTo(79.848724, 6);
    expect(tx.settlementCurrency).toBe("GBP");
    // Not a cash transaction, so no trading currency is asserted.
    expect(tx.tradingCurrency).toBe("");
    expect(tx.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.ISIN, "IE00B3XXRP09"],
      [IdentifierType.SEDOL, "B7NLLS3"],
    ]);
  });

  it("negates a disposal", () => {
    const result = convertFidelityJson(
      json({ ...BUY, transactionType: "Sell", debitCreditIndicator: "DEBIT" })
    );
    expect(result.txs[0]!.quantity).toBe(-913);
  });

  it("takes a cash movement's value from valuation, signed by the indicator", () => {
    const result = convertFidelityJson(json(SERVICE_FEE));
    const tx = result.txs[0]!;
    expect(tx.quantity).toBe(-5.2);
    expect(tx.type).toBe(TxType.INVEXPENSE);
    // Cash transactions carry the currency on both sides.
    expect(tx.tradingCurrency).toBe("GBP");
    expect(tx.settlementCurrency).toBe("GBP");
  });

  it("keeps a cash amount that reports zero units", () => {
    const result = convertFidelityJson(
      json({ ...SERVICE_FEE, transactionType: "Tax On Interest", units: 0, valuation: 0.2 })
    );
    expect(result.txs[0]!.quantity).toBe(-0.2);
  });

  it("nets a matched transfer pair to zero", () => {
    const out = {
      ...SERVICE_FEE,
      transactionType: "Transfer To Cash Management Account For Fees",
      debitCreditIndicator: "DEBIT",
      units: 2.3,
      valuation: 2.3,
    };
    const back = { ...out, transactionType: "Cash In Ring-fenced For Fees", debitCreditIndicator: "CREDIT" };
    const result = convertFidelityJson(json(out, back));
    expect(result.txs.reduce((sum, tx) => sum + tx.quantity, 0)).toBeCloseTo(0, 10);
  });

  it("emits no identifier hint for a cash pseudo-ISIN", () => {
    // These appear on Buy and Sell rows too, so the transaction type cannot be
    // used to decide whether a row names a real security.
    const result = convertFidelityJson(
      json({ ...BUY, isin: "AA00S0000000", sedol: "S000000", assetName: "Cash" })
    );
    expect(result.txs[0]!.identifierHints).toEqual([]);
  });

  it("falls back to the order date when a transaction has not settled", () => {
    const result = convertFidelityJson(json({ ...SERVICE_FEE, settlementDate: null }));
    const ts = result.txs[0]!.timestamp!;
    // dealDate is 03/07/2026, built as local midnight.
    expect(Number(ts.seconds)).toBe(Math.floor(new Date(2026, 6, 3).getTime() / 1000));
  });

  it("skips cancelled transactions", () => {
    const result = convertFidelityJson(
      json({ ...BUY, status: "Cancelled", units: 0, valuation: 0 }, SERVICE_FEE)
    );
    expect(result.txs).toHaveLength(1);
    expect(result.errors).toEqual([]);
  });

  it("reports an unrecognised transaction type by name", () => {
    const result = convertFidelityJson(json({ ...SERVICE_FEE, transactionType: "Corporate Action" }));
    expect(result.txs).toEqual([]);
    expect(result.errors[0]!.message).toContain("Corporate Action");
    expect(result.errors[0]!.rowIndex).toBe(1);
  });

  it("reports a period spanning the rows it parsed", () => {
    const result = convertFidelityJson(json(BUY, SERVICE_FEE));
    expect(result.periodFrom).toEqual(new Date(2026, 5, 4));
    // Exclusive: the day after the last row, so the last row is inside it.
    expect(result.periodBefore).toEqual(new Date(2026, 6, 9));
  });

  it("rejects a payload that is not a JSON array", () => {
    expect(convertFidelityJson("<html>signed out</html>").errors[0]!.message).toContain("not JSON");
    expect(convertFidelityJson('{"error":"nope"}').errors[0]!.message).toContain("array");
  });
});

describe("convertFidelityJson grouping", () => {
  const trade = (fields: Record<string, unknown>) => ({
    accountNumber: "ACC-1",
    currency: "GBP",
    status: "Completed",
    dealDate: "08/02/2022",
    settlementDate: "10/02/2022",
    ...fields,
  });

  it("pairs a sell with the cash in of the same amount", () => {
    const result = convertFidelityJson(
      json(
        trade({
          transactionType: "Sell",
          assetName: "WISE PLC (WISE)",
          units: 1242,
          valuation: 7266.49,
          pricePerUnit: 5.85,
          debitCreditIndicator: "DEBIT",
          referenceId: "563466569",
        }),
        trade({
          transactionType: "Cash In From Sell",
          assetName: null,
          units: 7266.49,
          valuation: 7266.49,
          debitCreditIndicator: "CREDIT",
          referenceId: "563466571",
        })
      )
    );

    expect(result.txs).toHaveLength(2);
    expect(result.txs[0]!.groupRef).toBe("563466569");
    expect(result.txs[1]!.groupRef).toBe("563466569");
  });

  it("pairs a buy with the cash out despite the fee gap", () => {
    const result = convertFidelityJson(
      json(
        trade({
          transactionType: "Cash Out For Buy",
          assetName: null,
          units: 7380.19,
          valuation: 7380.19,
          debitCreditIndicator: "DEBIT",
          referenceId: "563466631",
        }),
        trade({
          transactionType: "Buy",
          assetName: "INVESCO EQQQ (EQQQ)",
          units: 28,
          valuation: 7390.19,
          pricePerUnit: 263.58,
          debitCreditIndicator: "CREDIT",
          referenceId: "563466632",
        })
      )
    );

    expect(result.txs[0]!.groupRef).toBe("563466632");
    expect(result.txs[1]!.groupRef).toBe("563466632");
  });

  it("leaves a separately reported charge ungrouped", () => {
    const result = convertFidelityJson(
      json(
        trade({
          transactionType: "Dealing Fee",
          assetName: null,
          units: 10,
          valuation: 10,
          debitCreditIndicator: "DEBIT",
          referenceId: "563466600",
        })
      )
    );

    expect(result.txs[0]!.groupRef).toBe("");
  });

  it("leaves rows ungrouped when the payload carries no reference", () => {
    const result = convertFidelityJson(
      json(
        trade({
          transactionType: "Sell",
          assetName: "WISE PLC (WISE)",
          units: 1242,
          valuation: 7266.49,
          debitCreditIndicator: "DEBIT",
        }),
        trade({
          transactionType: "Cash In From Sell",
          assetName: null,
          units: 7266.49,
          valuation: 7266.49,
          debitCreditIndicator: "CREDIT",
        })
      )
    );

    for (const tx of result.txs) {
      expect(tx.groupRef).toBe("");
    }
  });
});
