import { describe, it, expect } from "vitest";
import { extractIbkrSplits } from "./ibkr-ofx";

function buildOfx(transactions: string, secList = ""): string {
  return `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX>
  <SIGNONMSGSRSV1><SONRS><STATUS><CODE>0</CODE></STATUS></SONRS></SIGNONMSGSRSV1>
  <INVSTMTMSGSRSV1>
    <INVSTMTTRNRS>
      <INVSTMTRS>
        <CURDEF>USD
        <INVACCTFROM><BROKERID>4705</BROKERID><ACCTID>U999999</ACCTID></INVACCTFROM>
        <INVTRANLIST>
          <DTSTART>20220101
          <DTEND>20221231
          ${transactions}
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
  ${secList}
</OFX>`;
}

describe("extractIbkrSplits", () => {
  it("returns empty result for empty/invalid file", () => {
    const result = extractIbkrSplits("not an ofx file");
    expect(result.splits).toEqual([]);
    expect(result.errors.length).toBeGreaterThan(0);
  });

  it("extracts a SPLIT N FOR M ratio from a transaction MEMO", () => {
    const ofx = buildOfx(`
      <BUYSTOCK>
        <INVBUY>
          <INVTRAN>
            <FITID>1</FITID>
            <DTTRADE>20220606
            <MEMO>AMZN(US0231351067) SPLIT 20 FOR 1 (AMZN, AMAZON.COM INC, US0231351067)
          </INVTRAN>
          <SECID><UNIQUEID>US0231351067</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
          <UNITS>0
          <UNITPRICE>0
          <TOTAL>0
          <CURRENCY><CURRATE>1</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
        </INVBUY>
        <BUYTYPE>BUY
      </BUYSTOCK>
    `);
    const result = extractIbkrSplits(ofx);
    expect(result.errors).toEqual([]);
    expect(result.splits).toHaveLength(1);
    const s = result.splits[0]!;
    expect(s.exDate).toBe("2022-06-06");
    expect(s.splitFrom).toBe("1");
    expect(s.splitTo).toBe("20");
    expect(s.identifier.type).toBe("ISIN");
    expect(s.identifier.value).toBe("US0231351067");
    expect(s.account).toBe("U999999");
  });

  it("extracts ratio from a structured OFX SPLIT element", () => {
    const ofx = buildOfx(`
      <SPLIT>
        <INVTRAN>
          <FITID>2</FITID>
          <DTTRADE>20220720
        </INVTRAN>
        <SECID><UNIQUEID>US88160R1014</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
        <NUMERATOR>3
        <DENOMINATOR>1
      </SPLIT>
    `);
    const result = extractIbkrSplits(ofx);
    expect(result.errors).toEqual([]);
    expect(result.splits).toHaveLength(1);
    expect(result.splits[0]!.splitFrom).toBe("1");
    expect(result.splits[0]!.splitTo).toBe("3");
    expect(result.splits[0]!.exDate).toBe("2022-07-20");
  });

  it("dedupes the same split appearing in multiple records", () => {
    const memo = "AMZN(US0231351067) SPLIT 20 FOR 1";
    const ofx = buildOfx(`
      <BUYSTOCK>
        <INVBUY>
          <INVTRAN><FITID>1</FITID><DTTRADE>20220606<MEMO>${memo}</INVTRAN>
          <SECID><UNIQUEID>US0231351067</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
          <UNITS>0<UNITPRICE>0<TOTAL>0
          <CURRENCY><CURRATE>1</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
        </INVBUY>
        <BUYTYPE>BUY
      </BUYSTOCK>
      <BUYSTOCK>
        <INVBUY>
          <INVTRAN><FITID>2</FITID><DTTRADE>20220606<MEMO>${memo}</INVTRAN>
          <SECID><UNIQUEID>US0231351067</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
          <UNITS>0<UNITPRICE>0<TOTAL>0
          <CURRENCY><CURRATE>1</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
        </INVBUY>
        <BUYTYPE>BUY
      </BUYSTOCK>
    `);
    const result = extractIbkrSplits(ofx);
    expect(result.splits).toHaveLength(1);
  });

  it("ignores transactions without a SPLIT memo or SPLIT element", () => {
    const ofx = buildOfx(`
      <BUYSTOCK>
        <INVBUY>
          <INVTRAN><FITID>1</FITID><DTTRADE>20220101<MEMO>Regular buy</INVTRAN>
          <SECID><UNIQUEID>US0231351067</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
          <UNITS>10<UNITPRICE>100<TOTAL>-1000
          <CURRENCY><CURRATE>1</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
        </INVBUY>
        <BUYTYPE>BUY
      </BUYSTOCK>
    `);
    const result = extractIbkrSplits(ofx);
    expect(result.splits).toEqual([]);
  });

  it("rejects a SPLIT element with non-positive ratio", () => {
    const ofx = buildOfx(`
      <SPLIT>
        <INVTRAN><FITID>3</FITID><DTTRADE>20220720</INVTRAN>
        <SECID><UNIQUEID>X</UNIQUEID><UNIQUEIDTYPE>ISIN</UNIQUEIDTYPE></SECID>
        <NUMERATOR>0
        <DENOMINATOR>1
      </SPLIT>
    `);
    const result = extractIbkrSplits(ofx);
    expect(result.splits).toEqual([]);
    expect(result.errors.some((e) => /Invalid split ratio/.test(e.message))).toBe(true);
  });
});
