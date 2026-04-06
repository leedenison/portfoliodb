import { describe, it, expect } from "vitest";
import { parseOfxStatement, parseOfxDate } from "./parser";
import { TxType, IdentifierType } from "@/gen/api/v1/api_pb";

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
    expect(result.txs.length).toBe(0);
  });

  it("parses a single BUYSTOCK with CUSIP identifier", () => {
    const ofx = buildOfx({
      transactions: buyStockTx(),
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("023135106", "CUSIP", "AMZN AMAZON.COM INC", "AMZN")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(1);

    const tx = result.txs[0]!;
    expect(tx.type).toBe(TxType.BUYSTOCK);
    expect(tx.quantity).toBe(20);
    expect(tx.unitPrice).toBe(156.55);
    expect(tx.tradingCurrency).toBe("USD");
    expect(tx.account).toBe("U123");
    expect(tx.instrumentDescription).toBe("AMZN AMAZON.COM INC");
    expect(tx.identifierHints.length).toBe(1);
    expect(tx.identifierHints[0]!.type).toBe(IdentifierType.CUSIP);
    expect(tx.identifierHints[0]!.value).toBe("023135106");
  });

  it("parses a SELLSTOCK with ISIN identifier", () => {
    const ofx = buildOfx({
      transactions: sellStockTx(),
      secList: `<SECLISTMSGSRSV1><SECLIST>${stockSecList("IE00B4ND3602", "ISIN", "SGLN ISHARES PHYSICAL GOLD ETC", "SGLN")}</SECLIST></SECLISTMSGSRSV1>`,
    });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(1);

    const tx = result.txs[0]!;
    expect(tx.type).toBe(TxType.SELLSTOCK);
    expect(tx.quantity).toBe(-1230);
    expect(tx.instrumentDescription).toBe("SGLN ISHARES PHYSICAL GOLD ETC");
    expect(tx.identifierHints[0]!.type).toBe(IdentifierType.ISIN);
    expect(tx.identifierHints[0]!.value).toBe("IE00B4ND3602");
  });

  it("parses multiple transactions of the same type", () => {
    const txs = buyStockTx({ fitId: "1", units: "10" }) + buyStockTx({ fitId: "2", units: "20" });
    const ofx = buildOfx({ transactions: txs });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(2);
    expect(result.txs[0]!.quantity).toBe(10);
    expect(result.txs[1]!.quantity).toBe(20);
  });

  it("parses mixed buy and sell transactions", () => {
    const txs = buyStockTx({ fitId: "1" }) + sellStockTx({ fitId: "2" });
    const ofx = buildOfx({ transactions: txs });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(2);
    expect(result.txs.some((t) => t.type === TxType.BUYSTOCK)).toBe(true);
    expect(result.txs.some((t) => t.type === TxType.SELLSTOCK)).toBe(true);
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
    expect(result.txs.length).toBe(1);

    const tx = result.txs[0]!;
    expect(tx.type).toBe(TxType.INCOME);
    expect(tx.quantity).toBe(137.08);
    expect(tx.unitPrice).toBe(1);
    expect(tx.instrumentDescription).toBe("MSFT MICROSOFT CORP");
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
    expect(result.txs.length).toBe(1);

    const tx = result.txs[0]!;
    expect(tx.type).toBe(TxType.BUYOPT);
    expect(tx.quantity).toBe(3);
    expect(tx.instrumentDescription).toContain("BRKB");
    // CONID is not a standard identifier -- no hints from generic parser.
    // Broker-specific converters (e.g. IBKR) add OCC hints via post-processing.
    expect(tx.identifierHints.length).toBe(0);
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
    expect(result.txs[0]!.tradingCurrency).toBe("EUR");
  });

  it("falls back to UNIQUEID for description when SECLIST missing", () => {
    const ofx = buildOfx({ transactions: buyStockTx() });
    const result = parseOfxStatement(ofx);
    expect(result.errors).toEqual([]);
    expect(result.txs[0]!.instrumentDescription).toBe("023135106");
  });

  it("extracts period from DTSTART/DTEND", () => {
    const ofx = buildOfx({
      dtStart: "20260107202000.000[-5:EST]",
      dtEnd: "20260403202000.000[-4:EDT]",
      transactions: buyStockTx(),
    });
    const result = parseOfxStatement(ofx);
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodTo.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBeLessThan(result.periodTo.getMonth());
  });
});
