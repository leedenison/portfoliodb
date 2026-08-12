import { Big } from "@/lib/decimal";
import { describe, it, expect } from "vitest";
import { parseOfxStatement, parseOfxDate } from "./parser";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";
import { expectGroupsBalance } from "@/lib/csv/group-balance.test-utils";
import { RECORD_LABEL } from "@/lib/csv/postings";

describe("parseOfxDate", () => {
  it("parses full datetime with negative offset", () => {
    const d = parseOfxDate("20260121112611.000[-5:EST]");
    expect(d).not.toBeNull();
    expect(d!.toISOString()).toBe("2026-01-21T16:26:11.000Z"); // -5h offset -> UTC
  });

  it("parses full datetime with negative offset (EDT)", () => {
    const d = parseOfxDate("20260312202000.000[-4:EDT]");
    expect(d).not.toBeNull();
    expect(d!.toISOString()).toContain("2026-03-13T00:20:00.000Z");
  });

  it("parses date-only format", () => {
    const d = parseOfxDate("20260116");
    expect(d).not.toBeNull();
    expect(d!.getFullYear()).toBe(2026);
    expect(d!.getMonth()).toBe(0);
    expect(d!.getDate()).toBe(16);
  });

  it("returns null for empty string", () => {
    expect(parseOfxDate("")).toBeNull();
    expect(parseOfxDate("  ")).toBeNull();
  });
});

/** Helper to build a minimal OFX document for testing. */
function buildOfx({
  curdef = "GBP",
  acctId = "U123",
  dtStart = "20260101",
  dtEnd = "20260401",
  transactions = "",
  secList = "",
}: {
  curdef?: string;
  acctId?: string;
  dtStart?: string;
  dtEnd?: string;
  transactions?: string;
  secList?: string;
}): string {
  return `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX>
  <SIGNONMSGSRSV1><SONRS><STATUS><CODE>0</CODE></STATUS></SONRS></SIGNONMSGSRSV1>
  <INVSTMTMSGSRSV1>
    <INVSTMTTRNRS>
      <INVSTMTRS>
        <CURDEF>${curdef}
        <INVACCTFROM><BROKERID>4705</BROKERID><ACCTID>${acctId}</ACCTID></INVACCTFROM>
        <INVTRANLIST>
          <DTSTART>${dtStart}
          <DTEND>${dtEnd}
          ${transactions}
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
  ${secList}
</OFX>`;
}

function buyStockTx({
  fitId = "1",
  date = "20260303151138.000[-5:EST]",
  uniqueId = "023135106",
  uniqueIdType = "CUSIP",
  units = "20",
  unitPrice = "156.55",
  curSym = "USD",
}: Partial<Record<string, string>> = {}): string {
  return `<BUYSTOCK>
    <INVBUY>
      <INVTRAN><FITID>${fitId}</FITID><DTTRADE>${date}</DTTRADE></INVTRAN>
      <SECID><UNIQUEID>${uniqueId}</UNIQUEID><UNIQUEIDTYPE>${uniqueIdType}</UNIQUEIDTYPE></SECID>
      <UNITS>${units}
      <UNITPRICE>${unitPrice}
      <TOTAL>-3131
      <CURRENCY><CURRATE>0.75</CURRATE><CURSYM>${curSym}</CURSYM></CURRENCY>
    </INVBUY>
    <BUYTYPE>BUY
  </BUYSTOCK>`;
}

function sellStockTx({
  fitId = "2",
  date = "20260121112611.000[-5:EST]",
  uniqueId = "IE00B4ND3602",
  uniqueIdType = "ISIN",
  units = "-1230",
  unitPrice = "69.90",
  curSym = "GBP",
}: Partial<Record<string, string>> = {}): string {
  return `<SELLSTOCK>
    <INVSELL>
      <INVTRAN><FITID>${fitId}</FITID><DTTRADE>${date}</DTTRADE></INVTRAN>
      <SECID><UNIQUEID>${uniqueId}</UNIQUEID><UNIQUEIDTYPE>${uniqueIdType}</UNIQUEIDTYPE></SECID>
      <UNITS>${units}
      <UNITPRICE>${unitPrice}
      <TOTAL>85939
      <CURRENCY><CURRATE>1.0</CURRATE><CURSYM>${curSym}</CURSYM></CURRENCY>
    </INVSELL>
    <SELLTYPE>SELL
  </SELLSTOCK>`;
}

function stockSecList(uniqueId: string, uniqueIdType: string, secName: string, ticker: string): string {
  return `<STOCKINFO><SECINFO>
    <SECID><UNIQUEID>${uniqueId}</UNIQUEID><UNIQUEIDTYPE>${uniqueIdType}</UNIQUEIDTYPE></SECID>
    <SECNAME>${secName}</SECNAME>
    <TICKER>${ticker}</TICKER>
  </SECINFO></STOCKINFO>`;
}

function optSecList(conId: string, secName: string, ticker: string): string {
  return `<OPTINFO><SECINFO>
    <SECID><UNIQUEID>${conId}</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
    <SECNAME>${secName}</SECNAME>
    <TICKER>${ticker}</TICKER>
  </SECINFO><OPTTYPE>PUT</OPTTYPE><STRIKEPRICE>470</STRIKEPRICE></OPTINFO>`;
}

describe("parseOfxStatement", () => {
  it("returns error for non-OFX content", () => {
    const result = parseOfxStatement("just some text");
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.postings.length).toBe(0);
  });

  it("parses a single BUYSTOCK with CUSIP identifier", () => {
    const ofx = buildOfx({
      transactions: buyStockTx(),
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("023135106", "CUSIP", "AMZN AMAZON.COM INC", "AMZN")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(2); // security tx + cashflow

    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(tx.assetClassHint).toBe(AssetClass.STOCK);
    expect(tx.quantity).toBe("20");
    expect(tx.unitPrice).toBe("156.55");
    expect(tx.tradingCurrency).toBe("USD");
    expect(tx.account).toBe("U123");
    expect(tx.instrumentDescription).toBe("AMZN AMAZON.COM INC");
    expect(tx.identifierHints.length).toBe(1);
    expect(tx.identifierHints[0]!.type).toBe(IdentifierType.CUSIP);
    expect(tx.identifierHints[0]!.value).toBe("023135106");

    const cf = result.postings[1]!;
    expect(cf.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(cf.assetClassHint).toBe(AssetClass.CASH);
    expect(cf.quantity).toBe("-3131");
    expect(cf.unitPrice).toBe("1");
    expect(cf.tradingCurrency).toBe("USD");
    expect(cf.identifierHints.length).toBe(1);
    expect(cf.identifierHints[0]!.type).toBe(IdentifierType.CURRENCY);
    expect(cf.identifierHints[0]!.value).toBe("USD");
  });

  it("parses a SELLSTOCK with ISIN identifier", () => {
    const ofx = buildOfx({
      transactions: sellStockTx(),
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("IE00B4ND3602", "ISIN", "SGLN ISHARES PHYSICAL GOLD ETC", "SGLN")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(2); // security tx + cashflow

    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(tx.quantity).toBe("-1230");
    expect(tx.instrumentDescription).toBe("SGLN ISHARES PHYSICAL GOLD ETC");
    expect(tx.identifierHints[0]!.type).toBe(IdentifierType.ISIN);
    expect(tx.identifierHints[0]!.value).toBe("IE00B4ND3602");

    const cf = result.postings[1]!;
    expect(cf.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(cf.quantity).toBe("85939");
    expect(cf.unitPrice).toBe("1");
  });

  it("parses multiple transactions of the same type", () => {
    const txs = buyStockTx({ fitId: "1", units: "10" }) + buyStockTx({ fitId: "2", units: "20" });
    const ofx = buildOfx({ transactions: txs });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    // 2 security txs + 2 cashflow txs = 4
    const buys = result.postings.filter((t) => mustBe(t.brokerTxType, TxType.TRADE_ASSET));
    expect(buys.length).toBe(2);
    expect(buys[0]!.quantity).toBe("10");
    expect(buys[1]!.quantity).toBe("20");
  });

  it("parses mixed buy and sell transactions", () => {
    const txs = buyStockTx({ fitId: "1" }) + sellStockTx({ fitId: "2" });
    const ofx = buildOfx({ transactions: txs });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    // 2 security txs + 2 cashflow txs = 4. The direction is the quantity's
    // sign now rather than a type of its own.
    const assets = result.postings.filter((t) => mustBe(t.brokerTxType, TxType.TRADE_ASSET));
    expect(assets.some((t) => new Big(t.quantity).gt(0))).toBe(true);
    expect(assets.some((t) => new Big(t.quantity).lt(0))).toBe(true);
    expect(result.postings.filter((t) => mustBe(t.brokerTxType, TxType.TRADE_CASH)).length).toBe(2);
  });

  it("parses INCOME with TOTAL as quantity and price=1", () => {
    const income = `<INCOME>
      <INVTRAN><FITID>div1</FITID><DTTRADE>20260312202000.000[-4:EDT]</DTTRADE>
        <MEMO>MSFT CASH DIVIDEND USD 0.91</MEMO></INVTRAN>
      <SECID><UNIQUEID>594918104</UNIQUEID><UNIQUEIDTYPE>CUSIP</UNIQUEIDTYPE></SECID>
      <INCOMETYPE>DIV
      <TOTAL>137.08
      <CURRENCY><CURRATE>0.75</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
    </INCOME>`;
    const ofx = buildOfx({
      transactions: income,
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("594918104", "CUSIP", "MSFT MICROSOFT CORP", "MSFT")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    // The cash row alone. The income it came from is named by the declared type,
    // so the server posts it.
    expect(result.postings.length).toBe(1);

    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.INCOME]);
    expect(tx.assetClassHint).toBe(AssetClass.CASH);
    expect(tx.quantity).toBe("137.08");
    expect(tx.unitPrice).toBe("1");
    expect(tx.instrumentDescription).toBe("MSFT MICROSOFT CORP");
    expect(tx.identifierHints.length).toBe(1);
    expect(tx.identifierHints[0]!.type).toBe(IdentifierType.CURRENCY);
    expect(tx.identifierHints[0]!.value).toBe("USD");
  });

  it("parses BUYOPT with CONID (no identifier hint -- broker-specific post-processing needed)", () => {
    const buyOpt = `<BUYOPT>
      <INVBUY>
        <INVTRAN><FITID>opt1</FITID><DTTRADE>20260303151000.000[-5:EST]</DTTRADE></INVTRAN>
        <SECID><UNIQUEID>786977282</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
        <UNITS>3
        <UNITPRICE>14.12
        <TOTAL>-4239
        <CURRENCY><CURRATE>0.75</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
      </INVBUY>
      <OPTBUYTYPE>BUYTOOPEN
      <SHPERCTRCT>100
    </BUYOPT>`;
    const ofx = buildOfx({
      transactions: buyOpt,
      secList: `<SECLISTMSGSRSV1><SECLIST>${optSecList("786977282", "BRKB  260918P00470000 BRK B 18SEP26 470 P", "BRKB  260918P00470000")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(2); // security tx + cashflow

    const tx = result.postings[0]!;
    expect(tx.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(tx.assetClassHint).toBe(AssetClass.OPTION);
    expect(tx.quantity).toBe("3");
    expect(tx.instrumentDescription).toContain("BRKB");
    // CONID is not a standard identifier -- no hints from generic parser.
    // Broker-specific converters (e.g. IBKR) add OCC hints via post-processing.
    expect(tx.identifierHints.length).toBe(0);

    const cf = result.postings[1]!;
    expect(cf.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(cf.quantity).toBe("-4239");
  });

  it("returns secList for broker-specific post-processing", () => {
    const ofx = buildOfx({
      transactions: buyStockTx(),
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("023135106", "CUSIP", "AMZN AMAZON.COM INC", "AMZN")}${optSecList("786977282", "BRKB  260918P00470000 BRK B 18SEP26 470 P", "BRKB  260918P00470000")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.secList.size).toBe(2);
    expect(result.secList.get("023135106")?.ticker).toBe("AMZN");
    expect(result.secList.get("786977282")?.uniqueIdType).toBe("CONID");
  });

  it("uses account currency when transaction has no CURRENCY element", () => {
    const tx = `<BUYSTOCK>
      <INVBUY>
        <INVTRAN><FITID>1</FITID><DTTRADE>20260303</DTTRADE></INVTRAN>
        <SECID><UNIQUEID>IE00B4ND3602</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
        <UNITS>10
        <UNITPRICE>50
        <TOTAL>-500
      </INVBUY>
      <BUYTYPE>BUY
    </BUYSTOCK>`;
    const ofx = buildOfx({ curdef: "EUR", transactions: tx });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    const buy = result.postings.find((t) => mustBe(t.brokerTxType, TxType.TRADE_ASSET))!;
    expect(buy.tradingCurrency).toBe("EUR");
  });

  it("falls back to UNIQUEID for description when SECLIST missing", () => {
    const ofx = buildOfx({ transactions: buyStockTx() });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.postings[0]!.instrumentDescription).toBe("023135106");
  });

  it("returns error when no currency is available", () => {
    // Build OFX without CURDEF and without per-transaction CURRENCY.
    const ofx = `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX>
  <SIGNONMSGSRSV1><SONRS><STATUS><CODE>0</CODE></STATUS></SONRS></SIGNONMSGSRSV1>
  <INVSTMTMSGSRSV1>
    <INVSTMTTRNRS>
      <INVSTMTRS>
        <INVACCTFROM><BROKERID>4705</BROKERID><ACCTID>U123</ACCTID></INVACCTFROM>
        <INVTRANLIST>
          <DTSTART>20260101
          <DTEND>20260401
          <BUYSTOCK>
            <INVBUY>
              <INVTRAN><FITID>1</FITID><DTTRADE>20260303</DTTRADE></INVTRAN>
              <SECID><UNIQUEID>023135106</UNIQUEID><UNIQUEIDTYPE>CUSIP</UNIQUEIDTYPE></SECID>
              <UNITS>10
              <UNITPRICE>50
              <TOTAL>-500
            </INVBUY>
            <BUYTYPE>BUY
          </BUYSTOCK>
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
</OFX>`;
    const result = parseOfxStatement(ofx);
    expect(result.postings.length).toBe(0);
    expect(result.errors.length).toBe(1);
    expect(result.errors[0]!.field).toBe("CURRENCY");
  });

  it("extracts period from DTSTART/DTEND", () => {
    const ofx = buildOfx({
      dtStart: "20260107202000.000[-5:EST]",
      dtEnd: "20260403202000.000[-4:EDT]",
      transactions: buyStockTx(),
    });
    const result = parseOfxStatement(ofx);
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodBefore.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBeLessThan(result.periodBefore.getMonth());
  });

  it("extends the period past DTEND when a transaction falls outside it", () => {
    // Brokers are inconsistent about whether DTEND is exclusive. A DTEND that
    // stops short would leave the last day's rows outside the window meant to
    // replace them.
    const ofx = buildOfx({
      dtStart: "20260107202000.000[-5:EST]",
      dtEnd: "20260107202000.000[-5:EST]",
      transactions: buyStockTx(),
    });
    const result = parseOfxStatement(ofx);
    const lastTx = result.postings[result.postings.length - 1]!.timestamp!;
    expect(result.periodBefore.getTime()).toBeGreaterThan(
      Number(lastTx.seconds) * 1000
    );
  });
});

describe("groups and charges", () => {
  /**
   * Verbatim from local/masters/U1000001_20250107_20260107.qfx, which is the
   * arithmetic this has to reproduce: 378 x 61.06 + 11.54034 = 23092.22034, so
   * TOTAL is the consideration with the commission already in it.
   */
  const GBP_BUY = `<BUYSTOCK>
    <INVBUY>
      <INVTRAN><FITID>20251015U10000018371888432</FITID><DTTRADE>20251015092129.000[-4:EDT]</DTTRADE></INVTRAN>
      <SECID><UNIQUEID>IE00B4ND3602</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
      <UNITS>378
      <UNITPRICE>61.06
      <COMMISSION>11.54034
      <TAXES>0
      <TOTAL>-23092.22034
      <CURRENCY><CURRATE>1.0</CURRATE><CURSYM>GBP</CURSYM></CURRENCY>
    </INVBUY>
    <BUYTYPE>BUY</BUYTYPE>
  </BUYSTOCK>`;

  /** A sell in a currency that is not the account's: 10 x 609.472188 - 3.04736094. */
  const EUR_SELL = `<SELLSTOCK>
    <INVSELL>
      <INVTRAN><FITID>eur-sell-1</FITID><DTTRADE>20251016092129.000[-4:EDT]</DTTRADE></INVTRAN>
      <SECID><UNIQUEID>IE00B4ND3603</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
      <UNITS>-10
      <UNITPRICE>609.472188
      <COMMISSION>3.04736094
      <TOTAL>6091.67451906
      <CURRENCY><CURRATE>0.8432</CURRATE><CURSYM>EUR</CURSYM></CURRENCY>
    </INVSELL>
    <SELLTYPE>SELL</SELLTYPE>
  </SELLSTOCK>`;

  const parse = (transactions: string) => parseOfxStatement(buildOfx({ transactions }));

  it("groups a trade with its cash leg on the broker's own reference", () => {
    const result = parse(GBP_BUY);
    const refs = new Set(result.postings.map((t) => t.groupRef));
    expect(refs).toEqual(new Set(["20251015U10000018371888432"]));
  });

  // The cash that actually settled, charges included, which is not the cash leg
  // this trade settles through: that one is the consideration alone, because the
  // commission is posted as a leg of its own. The two differing by exactly the
  // charge is what makes this an independent figure rather than a restatement.
  it("transcribes TOTAL on the security leg, charge and all", () => {
    const result = parse(GBP_BUY);

    const security = result.postings.find((t) => mustBe(t.brokerTxType, TxType.TRADE_ASSET))!;
    expect(security.settlementAmount).toBe("23092.22034");

    const cash = result.postings.find((t) => mustBe(t.brokerTxType, TxType.TRADE_CASH))!;
    expect(cash.quantity).toBe("-23080.68");
    expect(cash.settlementAmount).toBeUndefined();
  });

  it("states nothing on an income row, whose quantity is already the amount", () => {
    const result = parse(`<INCOME>
      <INVTRAN><FITID>div-1</FITID><DTTRADE>20251016092129.000[-4:EDT]</DTTRADE></INVTRAN>
      <SECID><UNIQUEID>IE00B4ND3603</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
      <INCOMETYPE>DIV
      <TOTAL>137.08
      <CURRENCY><CURRATE>1.0</CURRATE><CURSYM>GBP</CURSYM></CURRENCY>
    </INCOME>`);

    const income = result.postings.find((t) => t.accountType !== AccountType.INCOME)!;
    expect(income.quantity).toBe("137.08");
    expect(income.settlementAmount).toBeUndefined();
  });

  it("splits the commission out of the netted total", () => {
    const result = parse(GBP_BUY);

    const cash = result.postings.find((t) => mustBe(t.brokerTxType, TxType.TRADE_CASH))!;
    // toBe, not toBeCloseTo: the split is a decimal subtraction, so the result
    // is the exact value narrowed once rather than an accumulation of error.
    expect(cash.quantity).toBe("-23080.68");

    // One posting, in the user's own account. Its expense side is named by the
    // declared type, so the server posts it.
    const fees = result.postings.filter((t) => mustBe(t.brokerTxType, TxType.EXPENSE));
    expect(fees).toHaveLength(1);
    expect(fees[0]!.accountType).toBe(AccountType.USER);
    expect(fees[0]!.quantity).toBe("-11.54034");
  });

  it("leaves the money that moved equal to the total the broker reported", () => {
    const result = parse(GBP_BUY);
    // Summed in decimal so the assertion holds for any fixture rather than for
    // ones whose float64 sum happens to land. This one's does.
    const cash = result.postings
      .filter((t) => !mustBe(t.brokerTxType, TxType.TRADE_ASSET))
      .reduce((sum, t) => sum.plus(t.quantity), new Big(0));
    expect(cash.toString()).toBe("-23092.22034");
  });

  it("balances a buy and a sell, including one not in the account's currency", () => {
    expectGroupsBalance(parse(GBP_BUY + EUR_SELL).postings);
  });

  it("derives the fee in the currency the trade was quoted in, not the account's", () => {
    // CURDEF is GBP and CURRATE is the rate back to it. UNITPRICE, COMMISSION
    // and TOTAL are all EUR, so nothing needs converting before the split.
    const fee = parse(EUR_SELL).postings.find(
      (t) => mustBe(t.brokerTxType, TxType.EXPENSE) && t.accountType === AccountType.USER
    )!;
    expect(fee.settlementCurrency).toBe("EUR");
    expect(fee.tradingCurrency).toBe("EUR");
    expect(fee.quantity).toBe("-3.04736094");
  });

  it("emits no charge postings for a commission-free trade", () => {
    const result = parse(buyStockTx());
    expect(result.postings.filter((t) => mustBe(t.brokerTxType, TxType.EXPENSE))).toHaveLength(0);
    expect(result.postings).toHaveLength(2);
  });

  it("groups a trade whose source gave no reference", () => {
    const result = parse(GBP_BUY.replace("<FITID>20251015U10000018371888432", "<FITID>"));
    const refs = new Set(result.postings.map((t) => t.groupRef));
    expect(refs.size).toBe(1);
    expect([...refs][0]).toBeTruthy();
    expectGroupsBalance(result.postings);
  });

  // The FITID is unique within the account rather than within the institution,
  // which is the uniqueness the OFX spec gives it, and it is opaque: the string
  // plainly carries a number but nothing here knows where it starts, so equality
  // is all it honestly offers.
  // Every leg read out of the record carries the record's id -- the security, the
  // cash derived from TOTAL and the fee derived from COMMISSION -- because that is
  // what the server puts the record back together from once nothing states the
  // grouping. The boundary leg is not read out of the record and carries nothing.
  it("states what the FITID is comparable by, on every leg of the record", () => {
    const result = parse(GBP_BUY);
    const correlated = result.postings.filter((t) => t.correlations.length !== 0);
    expect(correlated).toHaveLength(3);
    expect(correlated.map((p) => p.accountType)).not.toContain(AccountType.EXPENSE);
    for (const p of correlated) {
      const c = p.correlations[0]!;
      expect(c.token).toBe("20251015U10000018371888432");
      expect(c.scope).toBe(Scope.ACCOUNT);
      expect(c.match).toEqual([Match.EXACT]);
      expect(c.ordinal).toBeUndefined();
    }
  });

  // A record the source gave no FITID for still has to be reassemblable, so the
  // parser synthesises an identifier for it. It is file-scoped and says only that
  // these legs came out of one record -- which records are one event is not the
  // converter's to state.
  it("synthesises a record identifier for a trade the source did not reference", () => {
    const result = parse(GBP_BUY.replace("<FITID>20251015U10000018371888432", "<FITID>"));
    const correlated = result.postings.filter((t) => t.correlations.length !== 0);
    expect(correlated).toHaveLength(3);
    expect(new Set(correlated.map((p) => p.correlations[0]!.token)).size).toBe(1);
    const c = correlated[0]!.correlations[0]!;
    expect(c.label).toBe(RECORD_LABEL);
    expect(c.scope).toBe(Scope.FILE);
    expect(c.match).toEqual([Match.EXACT]);
  });

  it("emits a dividend as one posting, declared so the server can place its income", () => {
    const income = `<INCOME>
      <INVTRAN><FITID>div1</FITID><DTTRADE>20260312202000.000[-4:EDT]</DTTRADE>
        <MEMO>MSFT CASH DIVIDEND USD 0.91</MEMO></INVTRAN>
      <SECID><UNIQUEID>594918104</UNIQUEID><UNIQUEIDTYPE>CUSIP</UNIQUEIDTYPE></SECID>
      <INCOMETYPE>DIV
      <TOTAL>137.08
      <CURRENCY><CURRATE>0.75</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
    </INCOME>`;
    const result = parse(income);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0]!.quantity).toBe("137.08");
    expect(result.postings[0]!.groupRef).toBe("div1");
    expect(mustBe(result.postings[0]!.brokerTxType, TxType.INCOME)).toBe(true);
    expectGroupsBalance(result.postings);
  });
});
