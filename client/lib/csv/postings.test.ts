import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Tx } from "@/gen/api/v1/api_pb";
import { AccountType, IdentifierType, TxSchema, TxType } from "@/gen/api/v1/api_pb";
import { counterLeg, counterLegs, feeLeg, refPrefix, reinvestLeg, FEE_EPSILON } from "./postings";
import { Big } from "@/lib/decimal";
import { expectGroupsBalance } from "./group-balance.test-utils";

const tx = (fields: MessageInitShape<typeof TxSchema>): Tx =>
  create(TxSchema, {
    timestamp: timestampFromDate(new Date(2026, 1, 1)),
    instrumentDescription: "GBP",
    quantity: "0",
    account: "ACC-1",
    tradingCurrency: "GBP",
    settlementCurrency: "GBP",
    ...fields,
  });

describe("counterLeg", () => {
  it("sends a charge to expense and a dividend to income", () => {
    const charge = counterLeg(tx({ type: TxType.INVEXPENSE, quantity: "-3.24" }));
    expect(charge?.accountType).toBe(AccountType.EXPENSE);
    expect(charge?.quantity).toBe("3.24");

    const dividend = counterLeg(tx({ type: TxType.INCOME, quantity: "23.4" }));
    expect(dividend?.accountType).toBe(AccountType.INCOME);
    expect(dividend?.quantity).toBe("-23.4");
  });

  it("carries the group, account and currencies of the posting it mirrors", () => {
    const source = tx({
      type: TxType.INVEXPENSE,
      quantity: "-3.24",
      groupRef: "fee-1",
      account: "ACC-9",
      tradingCurrency: "EUR",
      settlementCurrency: "USD",
    });
    const leg = counterLeg(source)!;
    expect(leg.groupRef).toBe("fee-1");
    expect(leg.account).toBe("ACC-9");
    expect(leg.tradingCurrency).toBe("EUR");
    expect(leg.settlementCurrency).toBe("USD");
    expect(leg.timestamp).toEqual(source.timestamp);
  });

  it("leaves the posting it mirrors alone", () => {
    const source = tx({ type: TxType.INCOME, quantity: "23.4" });
    counterLeg(source);
    expect(source.quantity).toBe("23.4");
    expect(source.accountType).toBe(AccountType.UNSPECIFIED);
  });

  it("returns nothing for a type whose other side the source already supplied", () => {
    expect(counterLeg(tx({ type: TxType.BUYSTOCK, quantity: "10" }))).toBeUndefined();
    expect(counterLeg(tx({ type: TxType.CASHFLOW, quantity: "-10" }))).toBeUndefined();
  });

  it("returns nothing for a type whose other side is another account", () => {
    // A journal's pair arrives in a different statement, which is 0068's problem.
    expect(counterLeg(tx({ type: TxType.JRNLFUND, quantity: "500" }))).toBeUndefined();
    expect(counterLeg(tx({ type: TxType.TRANSFER, quantity: "500" }))).toBeUndefined();
  });

  it("does not mirror a counter-leg", () => {
    const leg = counterLeg(tx({ type: TxType.INVEXPENSE, quantity: "-3.24" }))!;
    expect(counterLeg(leg)).toBeUndefined();
  });
});

describe("feeLeg", () => {
  const trade = tx({ type: TxType.BUYSTOCK, quantity: "378", unitPrice: "61.06", groupRef: "t-1" });

  it("posts the commission as cash leaving the account", () => {
    const leg = feeLeg(trade, new Big("11.54034"))!;
    expect(leg.type).toBe(TxType.INVEXPENSE);
    expect(leg.quantity).toBe("-11.54034");
    expect(leg.unitPrice).toBe("1");
    expect(leg.accountType).toBe(AccountType.USER);
    expect(leg.groupRef).toBe("t-1");
    expect(leg.account).toBe("ACC-1");
  });

  it("is money in the settlement currency, not the instrument", () => {
    const source = tx({
      type: TxType.BUYSTOCK,
      quantity: "378",
      unitPrice: "61.06",
      tradingCurrency: "EUR",
      settlementCurrency: "USD",
    });
    const leg = feeLeg(source, new Big(5))!;
    expect(leg.instrumentDescription).toBe("USD");
    expect(leg.tradingCurrency).toBe("USD");
    expect(leg.settlementCurrency).toBe("USD");
    expect(leg.identifierHints[0]?.type).toBe(IdentifierType.CURRENCY);
    expect(leg.identifierHints[0]?.value).toBe("USD");
  });

  it("takes the magnitude, so a broker's sign convention does not flip it", () => {
    expect(feeLeg(trade, new Big("-11.54"))?.quantity).toBe("-11.54");
  });

  it("produces nothing below the tolerance the server routes at", () => {
    expect(feeLeg(trade, new Big(0))).toBeUndefined();
    expect(feeLeg(trade, FEE_EPSILON.div(2))).toBeUndefined();
    // A field the source did not supply, which parseDecimal reports as absent.
    expect(feeLeg(trade, undefined)).toBeUndefined();
    expect(feeLeg(trade, FEE_EPSILON)).toBeDefined();
  });

  it("balances a netted trade once its counter-leg is added", () => {
    // The broker reported -23092.22034 of cash with 11.54034 of it commission.
    const legs = [
      tx({ type: TxType.BUYSTOCK, quantity: "378", unitPrice: "61.06", groupRef: "t-1", instrumentDescription: "VUSA" }),
      tx({ type: TxType.CASHFLOW, quantity: "-23080.68", unitPrice: "1", groupRef: "t-1" }),
    ];
    legs.push(feeLeg(legs[0]!, new Big("11.54034"))!);
    legs.push(...counterLegs(legs));

    expectGroupsBalance(legs);
    // The two cash postings in the user's own account still sum to the total the
    // broker reported; splitting them changed where the money is attributed,
    // not how much of it moved.
    // Exact end to end: the postings carry decimal strings, so this sums them
    // as decimals and asserts the total rather than a neighbourhood of it.
    const cash = legs
      .filter((l) => l.accountType !== AccountType.EXPENSE && l.type !== TxType.BUYSTOCK)
      .reduce((sum, l) => sum.plus(l.quantity), new Big(0));
    expect(cash.toString()).toBe("-23092.22034");
  });
});

describe("reinvestLeg", () => {
  // Quantities from the one reinvestment in the sample exports: 21.09 units of a
  // fund at 1.50, against a printed total of 31.65.
  const reinvest = tx({
    type: TxType.REINVEST,
    instrumentDescription: "Baillie Gifford Responsible Global Equity Income B Inc",
    quantity: "21.09",
    unitPrice: "1.5",
    groupRef: "582193319",
  });

  it("names the income the units cost, in the income account", () => {
    const leg = reinvestLeg(reinvest)!;
    expect(leg.type).toBe(TxType.INCOME);
    expect(leg.accountType).toBe(AccountType.INCOME);
    // Quantity times price, not the total the broker printed: it is the weight of
    // the posting this balances, so the group comes out at exactly zero.
    expect(leg.quantity).toBe("-31.635");
    expect(leg.unitPrice).toBe("1");
    expect(leg.groupRef).toBe("582193319");
    // Money, so it resolves to the currency rather than to the fund.
    expect(leg.instrumentDescription).toBe("GBP");
    expect(leg.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
    expectGroupsBalance([reinvest, leg]);
  });

  it("leaves every other type to counterLeg", () => {
    expect(reinvestLeg(tx({ type: TxType.BUYSTOCK, quantity: "10", unitPrice: "5" }))).toBeUndefined();
    expect(reinvestLeg(tx({ type: TxType.INCOME, quantity: "10" }))).toBeUndefined();
  });

  it("posts nothing for a reinvestment worth less than a rounding", () => {
    expect(reinvestLeg(tx({ type: TxType.REINVEST, quantity: "0.001", unitPrice: "1" }))).toBeUndefined();
    // A reinvestment reporting no price has no money to name.
    expect(reinvestLeg(tx({ type: TxType.REINVEST, quantity: "21.09" }))).toBeUndefined();
  });
});

describe("refPrefix", () => {
  it("avoids a ref the batch already uses", () => {
    expect(refPrefix([tx({ type: TxType.INCOME, groupRef: "p0" })])).toBe("p_");
    expect(
      refPrefix([tx({ type: TxType.INCOME, groupRef: "p0" }), tx({ type: TxType.INCOME, groupRef: "p_1" })])
    ).toBe("p__");
  });

  it("leaves broker refs that share no prefix alone", () => {
    expect(refPrefix([tx({ type: TxType.INCOME, groupRef: "563466569" })])).toBe("p");
  });
});

describe("counterLegs", () => {
  it("groups a one-sided row with the leg that balances it", () => {
    const txs = [tx({ type: TxType.INCOME, quantity: "23.4" })];
    txs.push(...counterLegs(txs));

    expect(txs).toHaveLength(2);
    expect(txs[0]!.groupRef).not.toBe("");
    expect(txs[1]!.groupRef).toBe(txs[0]!.groupRef);
    expectGroupsBalance(txs);
  });

  it("keeps a group the converter already assigned", () => {
    const txs = [tx({ type: TxType.INVEXPENSE, quantity: "-3.24", groupRef: "563466600" })];
    txs.push(...counterLegs(txs));
    expect(txs[1]!.groupRef).toBe("563466600");
  });

  it("gives each one-sided row a group of its own", () => {
    const txs = [
      tx({ type: TxType.INCOME, quantity: "23.4" }),
      tx({ type: TxType.INVEXPENSE, quantity: "-3.24" }),
    ];
    txs.push(...counterLegs(txs));
    expect(txs[0]!.groupRef).not.toBe(txs[1]!.groupRef);
    expectGroupsBalance(txs);
  });
});
