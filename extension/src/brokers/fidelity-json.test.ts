/**
 * Row shapes are taken from a real Fidelity export; account numbers and
 * quantities are altered, identifiers and the field structure are not.
 */

import { Big } from "@/lib/decimal";
import { describe, expect, it } from "vitest";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";
import { convertFidelityJson, isValidIsin } from "./fidelity-json";
import { expectGroupsBalance } from "@/lib/csv/group-balance.test-utils";

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
    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(tx.quantity).toBe("913");
    expect(tx.unitPrice).toBe("79.848724");
    expect(tx.settlementCurrency).toBe("GBP");
    // Not a cash transaction, so no trading currency is asserted.
    expect(tx.tradingCurrency).toBeUndefined();
    expect(tx.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.ISIN, "IE00B3XXRP09"],
      [IdentifierType.SEDOL, "B7NLLS3"],
    ]);
  });

  it("keeps a reported price of zero rather than dropping it as absent", () => {
    // A zero price is a price -- an option expiring worthless. Only a row that
    // reports none at all leaves unitPrice unset.
    const priced = convertFidelityJson(json({ ...BUY, pricePerUnit: 0 }));
    expect(priced.postings[0]!.unitPrice).toBe("0");

    const { pricePerUnit: _omitted, ...unpriced } = BUY;
    expect(convertFidelityJson(json(unpriced)).postings[0]!.unitPrice).toBeUndefined();
  });

  it("negates a disposal", () => {
    const result = convertFidelityJson(
      json({ ...BUY, transactionType: "Sell", debitCreditIndicator: "DEBIT" })
    );
    expect(result.postings[0]!.quantity).toBe("-913");
  });

  it("takes a cash movement's value from valuation, signed by the indicator", () => {
    const result = convertFidelityJson(json(SERVICE_FEE));
    const tx = result.postings[0]!;
    expect(tx.quantity).toBe("-5.2");
    expect(tx.brokerTxType).toEqual([TxType.HOLDING_COST]);
    expect(tx.assetClassHint).toBe(AssetClass.CASH);
    // Cash transactions carry the currency on both sides.
    expect(tx.tradingCurrency).toBe("GBP");
    expect(tx.settlementCurrency).toBe("GBP");
  });

  it("keeps a cash amount that reports zero units", () => {
    const result = convertFidelityJson(
      json({ ...SERVICE_FEE, transactionType: "Tax On Interest", units: 0, valuation: 0.2 })
    );
    expect(result.postings[0]!.quantity).toBe("-0.2");
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
    // Summed as decimals: the postings carry decimal strings now.
    const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
    expect(total.eq(0)).toBe(true);
  });

  it("resolves a cash posting to its currency, never to a pseudo-ISIN", () => {
    // The pseudo-identifiers appear on Buy and Sell rows too, so the transaction
    // type cannot be used to decide whether a row names a real security. Nothing
    // is passed on that would resolve to a fictional instrument.
    const result = convertFidelityJson(json(SERVICE_FEE));
    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
    expect(result.postings[0]!.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
  });

  it("describes a cash posting by its currency even when the row names a payer", () => {
    // assetName carries "Relationship Cash Source" on some rows and the paying
    // security's name on others. Either would resolve the money into a holding.
    const result = convertFidelityJson(
      json({ ...SERVICE_FEE, transactionType: "Cash Dividend", assetName: "Relationship Cash Source", isin: "AA00K0000000", debitCreditIndicator: "CREDIT" })
    );
    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
  });

  it("falls back to the order date when a transaction has not settled", () => {
    const result = convertFidelityJson(json({ ...SERVICE_FEE, settlementDate: null }));
    const ts = result.postings[0]!.timestamp!;
    // dealDate is 03/07/2026, built as local midnight.
    expect(Number(ts.seconds)).toBe(Math.floor(new Date(2026, 6, 3).getTime() / 1000));
  });

  it("skips cancelled transactions", () => {
    const result = convertFidelityJson(
      json({ ...BUY, status: "Cancelled", units: 0, valuation: 0 }, SERVICE_FEE)
    );
    // The service fee and its expense leg; nothing from the cancelled buy.
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.HOLDING_COST]);
    expect(result.errors).toEqual([]);
  });

  it("reports an unrecognised transaction type by name", () => {
    const result = convertFidelityJson(json({ ...SERVICE_FEE, transactionType: "Corporate Action" }));
    expect(result.postings).toEqual([]);
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

// Fidelity models an account's cash as a tradable asset, so money moving in or
// out is reported as a Buy or Sell of it against a pseudo-ISIN. Mapping on the
// transaction type alone made each one a security trade in units of money, and
// lost the movement the third row of the sequence records. Rows from the
// captured payload, AP10000001 on 18/05/2026 and AG10000001 on 15/04/2025.
describe("convertFidelityJson cash-asset rows", () => {
  const cash = (fields: Record<string, unknown>) => ({
    accountNumber: "AP10000001",
    assetName: "Cash",
    isin: "AA00R0000000",
    sedol: "R000000",
    pricePerUnit: 1,
    currency: "GBP",
    status: "Completed",
    dealDate: "18/05/2026",
    settlementDate: "18/05/2026",
    units: 12772.83,
    valuation: 12772.83,
    ...fields,
  });

  const BUY_CASH = cash({
    transactionType: "Buy",
    debitCreditIndicator: "CREDIT",
    referenceId: "1166853279",
  });
  const CASH_OUT = cash({
    transactionType: "Cash Out For Buy From Transfer",
    debitCreditIndicator: "DEBIT",
    referenceId: "1166853277",
  });
  const ARRIVAL = cash({
    transactionType: "Cash In For Transfer",
    debitCreditIndicator: "CREDIT",
    referenceId: "1166853278",
  });

  it("is a cash movement, not a security trade", () => {
    const result = convertFidelityJson(json(BUY_CASH));
    expect(result.errors).toEqual([]);
    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(tx.assetClassHint).toBe(AssetClass.CASH);
    expect(tx.quantity).toBe("12772.83");
    expect(tx.tradingCurrency).toBe("GBP");
    // Money, so it resolves to the currency and never to the pseudo-ISIN the row
    // carries.
    expect(tx.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
  });

  it("groups a purchase of cash with the cash out beside it", () => {
    const result = convertFidelityJson(json(CASH_OUT, BUY_CASH));
    expect(result.postings[0]!.groupRef).toBe("1166853279");
    expect(result.postings[1]!.groupRef).toBe("1166853279");
    expectGroupsBalance(result.postings);
  });

  it("leaves the transfer beside it as the money that arrived", () => {
    // All three rows are the same 12,772.83. Only the arrival is a movement;
    // the other two are the broker converting its own cash position and net to
    // zero, so the account must end that much up rather than level.
    const result = convertFidelityJson(json(CASH_OUT, ARRIVAL, BUY_CASH));
    expect(result.errors).toEqual([]);
    const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
    expect(total.eq(12772.83)).toBe(true);
    expect(result.postings.some((tx) => mustBe(tx.brokerTxType, TxType.TRADE_ASSET))).toBe(false);
  });

  it("reads a sale of cash the same way", () => {
    const sell = cash({
      accountNumber: "AG10000001",
      isin: "AA00S0000000",
      sedol: "S000000",
      transactionType: "Sell",
      debitCreditIndicator: "DEBIT",
      units: 20000,
      valuation: 20000,
      referenceId: "971613412",
    });
    const cashIn = cash({
      accountNumber: "AG10000001",
      isin: "AA00S0000000",
      sedol: "S000000",
      transactionType: "Cash In From Sell",
      debitCreditIndicator: "CREDIT",
      units: 20000,
      valuation: 20000,
      referenceId: "971613413",
    });

    const result = convertFidelityJson(json(sell, cashIn));
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(result.postings[0]!.quantity).toBe("-20000");
    expect(result.postings[0]!.groupRef).toBe("971613412");
    expect(result.postings[1]!.groupRef).toBe("971613412");
    expectGroupsBalance(result.postings);
  });

  it("still reads a purchase carrying a real identifier as a security trade", () => {
    const result = convertFidelityJson(json(BUY));
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
  });

  it("keeps a paired Cash In's declared set, with the row's own currency", () => {
    // Pairing no longer rewrites what the broker declared: the Cash In stays
    // {TRADE_CASH, TRANSFER} and the grouping alone records that it settled the
    // sale. The payload names a currency per row rather than taking one from an
    // upload option, so the cash leg reads it off the posting.
    const sale = cash({
      transactionType: "Sell For Switch",
      assetName: "M&G European Index Tracker",
      isin: "GB0002634946",
      sedol: "0263494",
      debitCreditIndicator: "DEBIT",
      units: 12147.03,
      valuation: 12091.15,
      referenceId: "661638570",
    });
    const proceeds = cash({
      transactionType: "Cash In",
      debitCreditIndicator: "CREDIT",
      units: 12091.15,
      valuation: 12091.15,
      referenceId: "661638574",
    });

    const result = convertFidelityJson(json(sale, proceeds));
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
    expect(result.postings[1]!.tradingCurrency).toBe("GBP");
    expect(result.postings[1]!.groupRef).toBe("661638570");
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
          referenceId: "441416452",
        }),
        trade({
          transactionType: "Cash In From Sell",
          assetName: null,
          units: 7266.49,
          valuation: 7266.49,
          debitCreditIndicator: "CREDIT",
          referenceId: "441416454",
        })
      )
    );

    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.groupRef).toBe("441416452");
    expect(result.postings[1]!.groupRef).toBe("441416452");
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
          referenceId: "441416514",
        }),
        trade({
          transactionType: "Buy",
          assetName: "INVESCO EQQQ (EQQQ)",
          units: 28,
          valuation: 7390.19,
          pricePerUnit: 263.58,
          debitCreditIndicator: "CREDIT",
          referenceId: "441416515",
        })
      )
    );

    expect(result.postings[0]!.groupRef).toBe("441416515");
    expect(result.postings[1]!.groupRef).toBe("441416515");
  });

  it("keeps a separately reported charge out of any trade's group", () => {
    const result = convertFidelityJson(
      json(
        trade({
          transactionType: "Dealing Fee",
          assetName: null,
          units: 10,
          valuation: 10,
          debitCreditIndicator: "DEBIT",
          referenceId: "441416483",
        })
      )
    );

    // A group of its own, holding the charge and the expense it went to.
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.groupRef).toBeTruthy();
    expect(result.postings[1]!.groupRef).toBe(result.postings[0]!.groupRef);
    expect(result.postings[1]!.accountType).toBe(AccountType.EXPENSE);
  });

  it("groups the run a deposit into a product account is reported through", () => {
    // The same shape the CSV route sees, since both call assignFidelityGroups:
    // the subscription credited, spent, and credited again as the money that
    // lands. Grouped, the account is left with one residual for one deposit.
    const deposit = (fields: Record<string, unknown>) =>
      trade({
        accountNumber: "AS10000001",
        assetName: "Cash",
        isin: "AA00S0000000",
        units: 20000,
        valuation: 20000,
        pricePerUnit: 1,
        dealDate: "15/04/2025",
        settlementDate: "15/04/2025",
        ...fields,
      });
    const result = convertFidelityJson(
      json(
        deposit({
          transactionType: "Cash In Lump Sum",
          debitCreditIndicator: "CREDIT",
          referenceId: "971613428",
        }),
        deposit({
          transactionType: "Cash Out For Buy",
          debitCreditIndicator: "DEBIT",
          referenceId: "971613429",
        }),
        deposit({
          transactionType: "Cash In",
          debitCreditIndicator: "CREDIT",
          referenceId: "971613431",
        })
      )
    );

    expect(result.errors).toEqual([]);
    expect(result.postings.map((tx) => tx.groupRef)).toEqual([
      "971613428",
      "971613428",
      "971613428",
    ]);
    // The declared sets survive grouping: the lump sum's TRANSFER is what routes
    // the group's residual to TRANSFER_CLEARING rather than IMBALANCE, and the
    // ambiguous Cash In keeps both its readings.
    expect(result.postings.map((tx) => tx.brokerTxType)).toEqual([
      [TxType.TRANSFER],
      [TxType.TRADE_CASH],
      [TxType.TRADE_CASH, TxType.TRANSFER],
    ]);
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

    for (const tx of result.postings) {
      expect(tx.groupRef).toBeUndefined();
    }
  });
});

// The JSON carries both series the format can express, and they are kept apart:
// a reference number is compared against another reference number, while the
// counterparty names an account and is compared against one.
describe("correlations", () => {
  const base = {
    accountNumber: "AW10000001",
    assetName: "Cash",
    isin: "AA00K0000000",
    currency: "GBP",
    status: "Completed",
    pricePerUnit: 1,
    dealDate: "15/04/2025",
    settlementDate: "15/04/2025",
  };

  it("states both series where the source supplies both", () => {
    const result = convertFidelityJson(
      json({
        ...base,
        transactionType: "Transfer Into Account",
        debitCreditIndicator: "CREDIT",
        units: 20000,
        valuation: 20000,
        referenceId: "971613414",
        sourceOrTargetAccount: "AG10000001",
      })
    );

    const got = result.postings[0]!.correlations;
    expect(got).toHaveLength(2);
    // Identical to what the CSV converter states for the same reference: the two
    // readings of one export share the builder, so they cannot disagree.
    expect(got[0]!.label).toBe("");
    expect(got[0]!.token).toBe("971613414");
    expect(got[0]!.ordinal).toBe(971613414n);
    expect(got[0]!.scope).toBe(Scope.FILE);
    expect(got[0]!.match).toEqual([Match.EXACT, Match.ORDINAL]);
    // The pointer is broker-scoped, because an account label means nothing
    // outside the broker that issued it and the two sides of one transfer
    // routinely arrive in different exports.
    expect(got[1]!.label).toBe("counterparty");
    expect(got[1]!.token).toBe("AG10000001");
    expect(got[1]!.scope).toBe(Scope.BROKER);
    expect(got[1]!.match).toEqual([Match.ACCOUNT]);
    expect(got[1]!.ordinal).toBeUndefined();
  });

  it("states only the reference where the source names no other side", () => {
    const result = convertFidelityJson(
      json({
        ...base,
        transactionType: "Transfer Out From Cash Management Account",
        debitCreditIndicator: "DEBIT",
        units: 20000,
        valuation: 20000,
        referenceId: "971613430",
      })
    );

    const got = result.postings[0]!.correlations;
    expect(got).toHaveLength(1);
    expect(got[0]!.token).toBe("971613430");
  });

  // Fidelity puts the product account a fee was charged for in the same field it
  // names a transfer's source in, so the pointer is stated either way: which of
  // the two it means is not the converter's call. A Service Fee is a holding cost
  // whose group balances against an EXPENSE leg, so it never produces the
  // TRANSFER_CLEARING residual that transfer matching reads a pointer for.
  //
  // A derived leg transcribes nothing, so it correlates with nothing -- and the
  // fee's attribution in particular must not be copied onto the expense leg,
  // where it would name a counterparty the source never gave that row.
  it("does not give a derived counter-leg a correlation", () => {
    const result = convertFidelityJson(
      json({
        ...base,
        assetName: "Relationship Cash Source",
        transactionType: "Service Fee",
        debitCreditIndicator: "DEBIT",
        units: 5.13,
        valuation: 5.13,
        referenceId: "1177083111",
        sourceOrTargetAccount: "AP10000001",
      })
    );

    expect(result.postings[0]!.brokerTxType).toEqual([TxType.HOLDING_COST]);
    expect(result.postings[0]!.correlations).toHaveLength(2);
    const expense = result.postings.find((tx) => tx.accountType === AccountType.EXPENSE)!;
    expect(expense.correlations).toEqual([]);
  });
});

describe("the source's own cash total", () => {
  // valuation, not units times pricePerUnit: 913 x 79.848724 is 72901.88, which
  // is the figure the cross-check compares against rather than the one grouping
  // matches on.
  it("transcribes valuation on a security row, unsigned", () => {
    const result = convertFidelityJson(json({ ...BUY, debitCreditIndicator: "DEBIT" }));

    expect(result.postings[0]!.settlementAmount).toBe("72909.39");
    expect(result.postings[0]!.quantity).toBe("-913");
  });

  // A cash row's quantity is already the valuation, so stating it again would
  // put the same figure on the posting twice.
  it("states nothing on a cash row", () => {
    const result = convertFidelityJson(
      json({
        accountNumber: "AW10000001",
        transactionType: "Cash In From Sell",
        assetName: "Cash",
        isin: "AA00K0000000",
        currency: "GBP",
        status: "Completed",
        pricePerUnit: 1,
        dealDate: "15/04/2025",
        settlementDate: "15/04/2025",
        debitCreditIndicator: "CREDIT",
        units: 20000,
        valuation: 20000,
      })
    );

    expect(result.postings[0]!.quantity).toBe("20000");
    expect(result.postings[0]!.settlementAmount).toBeUndefined();
  });
});
