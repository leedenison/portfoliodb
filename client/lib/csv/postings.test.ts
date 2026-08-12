import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Posting } from "@/gen/archive/v1/txs_pb";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import {
  feeLeg,
  identifyRecord,
  recordCorrelation,
  refPrefix,
  reinvestIncomeLeg,
  FEE_EPSILON,
  RECORD_LABEL,
} from "./postings";
import { mustBe } from "@/lib/tx-type";
import { Big } from "@/lib/decimal";
import { expectGroupsBalance } from "./group-balance.test-utils";

const tx = (fields: MessageInitShape<typeof PostingSchema>): Posting =>
  create(PostingSchema, {
    timestamp: timestampFromDate(new Date(2026, 1, 1)),
    instrumentDescription: "GBP",
    quantity: "0",
    account: "ACC-1",
    tradingCurrency: "GBP",
    settlementCurrency: "GBP",
    ...fields,
  });

describe("recordCorrelation", () => {
  it("is a file-scoped equality token under a label no broker issues", () => {
    const c = recordCorrelation("r7");
    expect(c.label).toBe(RECORD_LABEL);
    expect(c.token).toBe("r7");
    expect(c.scope).toBe(Scope.FILE);
    expect(c.match).toEqual([Match.EXACT]);
    expect(c.ordinal).toBeUndefined();
  });
});

describe("identifyRecord", () => {
  it("leaves a record its source identified alone", () => {
    const stated = recordCorrelation("ref-1");
    const p = tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "10", correlations: [stated] });
    expect(identifyRecord(p, "r7").correlations).toEqual([stated]);
  });

  it("identifies a record its source did not", () => {
    const p = tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "10" });
    expect(identifyRecord(p, "r7").correlations[0]!.token).toBe("r7");
  });
});

describe("feeLeg", () => {
  const trade = tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "378", unitPrice: "61.06", groupRef: "t-1" });

  it("posts the commission as cash leaving the account", () => {
    const leg = feeLeg(trade, new Big("11.54034"))!;
    expect(leg.brokerTxType).toEqual([TxType.TRANSACTION_COST]);
    expect(leg.assetClassHint).toBe(AssetClass.CASH);
    expect(leg.quantity).toBe("-11.54034");
    expect(leg.unitPrice).toBe("1");
    expect(leg.accountType).toBe(AccountType.USER);
    expect(leg.groupRef).toBe("t-1");
    expect(leg.account).toBe("ACC-1");
  });

  it("is money in the settlement currency, not the instrument", () => {
    const source = tx({
      brokerTxType: [TxType.TRADE_ASSET],
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

  // The commission is a field of the record the trade was read from, so the leg
  // built for it is another leg of that record and is found by the same token.
  it("carries the correlations of the record it was read out of", () => {
    const source = tx({
      brokerTxType: [TxType.TRADE_ASSET],
      quantity: "378",
      unitPrice: "61.06",
      correlations: [recordCorrelation("r7")],
    });
    expect(feeLeg(source, new Big("11.54034"))!.correlations[0]!.token).toBe("r7");
  });

  it("produces nothing below the tolerance the server routes at", () => {
    expect(feeLeg(trade, new Big(0))).toBeUndefined();
    expect(feeLeg(trade, FEE_EPSILON.div(2))).toBeUndefined();
    // A field the source did not supply, which parseDecimal reports as absent.
    expect(feeLeg(trade, undefined)).toBeUndefined();
    expect(feeLeg(trade, FEE_EPSILON)).toBeDefined();
  });

  it("balances a netted trade once the server posts its expense leg", () => {
    // The broker reported -23092.22034 of cash with 11.54034 of it commission.
    const legs = [
      tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "378", unitPrice: "61.06", groupRef: "t-1", instrumentDescription: "VUSA" }),
      tx({ brokerTxType: [TxType.TRADE_CASH], quantity: "-23080.68", unitPrice: "1", groupRef: "t-1" }),
    ];
    legs.push(feeLeg(legs[0]!, new Big("11.54034"))!);

    expectGroupsBalance(legs);
    // The two cash postings in the user's own account still sum to the total the
    // broker reported; splitting them changed where the money is attributed,
    // not how much of it moved.
    // Exact end to end: the postings carry decimal strings, so this sums them
    // as decimals and asserts the total rather than a neighbourhood of it.
    const cash = legs
      .filter((l) => !mustBe(l.brokerTxType, TxType.TRADE_ASSET))
      .reduce((sum, l) => sum.plus(l.quantity), new Big(0));
    expect(cash.toString()).toBe("-23092.22034");
  });
});

describe("reinvestIncomeLeg", () => {
  // Quantities from the one reinvestment in the sample exports: 21.09 units of a
  // fund at 1.50, against a printed total of 31.65.
  const reinvest = tx({
    brokerTxType: [TxType.TRADE_ASSET],
    instrumentDescription: "Baillie Gifford Responsible Global Equity Income B Inc",
    quantity: "21.09",
    unitPrice: "1.5",
    groupRef: "460143202",
  });

  it("names the income the units cost, in the income account", () => {
    const leg = reinvestIncomeLeg(reinvest)!;
    expect(leg.brokerTxType).toEqual([TxType.DIVIDEND]);
    expect(leg.assetClassHint).toBe(AssetClass.CASH);
    expect(leg.accountType).toBe(AccountType.INCOME);
    // Quantity times price, not the total the broker printed: it is the weight of
    // the posting this balances, so the group comes out at exactly zero.
    expect(leg.quantity).toBe("-31.635");
    expect(leg.unitPrice).toBe("1");
    expect(leg.groupRef).toBe("460143202");
    // Money, so it resolves to the currency rather than to the fund.
    expect(leg.instrumentDescription).toBe("GBP");
    expect(leg.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
    expectGroupsBalance([reinvest, leg]);
  });

  it("posts nothing for a reinvestment worth less than a rounding", () => {
    expect(
      reinvestIncomeLeg(tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "0.001", unitPrice: "1" }))
    ).toBeUndefined();
    // A reinvestment reporting no price has no money to name.
    expect(
      reinvestIncomeLeg(tx({ brokerTxType: [TxType.TRADE_ASSET], quantity: "21.09" }))
    ).toBeUndefined();
  });

  // The income is read out of the reinvestment record, which is the only thing
  // that says the units were bought with a dividend, so the two are found together
  // by the record's own token.
  it("carries the correlations of the record it was read out of", () => {
    const stated = recordCorrelation("460143202");
    const source = tx({
      brokerTxType: [TxType.TRADE_ASSET],
      quantity: "21.09",
      unitPrice: "1.5",
      correlations: [stated],
    });
    const leg = reinvestIncomeLeg(source)!;
    expect(leg.correlations).toEqual([stated]);
    // A copy: editing the leg's evidence must not edit the record's.
    expect(leg.correlations[0]).not.toBe(stated);
  });
});

describe("refPrefix", () => {
  it("avoids a ref the batch already uses", () => {
    expect(refPrefix([tx({ brokerTxType: [TxType.INCOME], groupRef: "p0" })])).toBe("p_");
    expect(
      refPrefix([
        tx({ brokerTxType: [TxType.INCOME], groupRef: "p0" }),
        tx({ brokerTxType: [TxType.INCOME], groupRef: "p_1" }),
      ])
    ).toBe("p__");
  });

  it("leaves broker refs that share no prefix alone", () => {
    expect(refPrefix([tx({ brokerTxType: [TxType.INCOME], groupRef: "441416452" })])).toBe("p");
  });
});
