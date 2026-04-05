import { describe, it, expect } from "vitest";
import { parseOfxSgml } from "./sgml";

describe("parseOfxSgml", () => {
  it("parses header key:value pairs", () => {
    const text = `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX></OFX>`;
    const { header } = parseOfxSgml(text);
    expect(header.OFXHEADER).toBe("100");
    expect(header.DATA).toBe("OFXSGML");
    expect(header.VERSION).toBe("102");
  });

  it("parses leaf values without closing tags", () => {
    const text = `<OFX><UNITS>100<UNITPRICE>50.5</OFX>`;
    const { body } = parseOfxSgml(text);
    const ofx = body.OFX as Record<string, unknown>;
    expect(ofx.UNITS).toBe("100");
    expect(ofx.UNITPRICE).toBe("50.5");
  });

  it("parses leaf values with closing tags", () => {
    const text = `<OFX><CODE>0</CODE><SEVERITY>INFO</SEVERITY></OFX>`;
    const { body } = parseOfxSgml(text);
    const ofx = body.OFX as Record<string, unknown>;
    expect(ofx.CODE).toBe("0");
    expect(ofx.SEVERITY).toBe("INFO");
  });

  it("parses nested container elements", () => {
    const text = `<OFX><INVACCTFROM><BROKERID>4705</BROKERID><ACCTID>U123</ACCTID></INVACCTFROM></OFX>`;
    const { body } = parseOfxSgml(text);
    const acct = (body.OFX as Record<string, unknown>).INVACCTFROM as Record<string, unknown>;
    expect(acct.BROKERID).toBe("4705");
    expect(acct.ACCTID).toBe("U123");
  });

  it("collects repeated siblings into arrays", () => {
    const text = `<OFX>
      <INVTRANLIST>
        <BUYSTOCK><INVBUY><UNITS>10</UNITS></INVBUY></BUYSTOCK>
        <BUYSTOCK><INVBUY><UNITS>20</UNITS></INVBUY></BUYSTOCK>
        <BUYSTOCK><INVBUY><UNITS>30</UNITS></INVBUY></BUYSTOCK>
      </INVTRANLIST>
    </OFX>`;
    const { body } = parseOfxSgml(text);
    const tranList = (body.OFX as Record<string, unknown>).INVTRANLIST as Record<string, unknown>;
    const buys = tranList.BUYSTOCK as Record<string, unknown>[];
    expect(Array.isArray(buys)).toBe(true);
    expect(buys.length).toBe(3);
    expect((buys[0].INVBUY as Record<string, unknown>).UNITS).toBe("10");
    expect((buys[2].INVBUY as Record<string, unknown>).UNITS).toBe("30");
  });

  it("handles mixed repeated and single siblings", () => {
    const text = `<OFX>
      <INVTRANLIST>
        <DTSTART>20260101
        <BUYSTOCK><INVBUY><UNITS>10</UNITS></INVBUY></BUYSTOCK>
        <SELLSTOCK><INVSELL><UNITS>-5</UNITS></INVSELL></SELLSTOCK>
        <BUYSTOCK><INVBUY><UNITS>20</UNITS></INVBUY></BUYSTOCK>
      </INVTRANLIST>
    </OFX>`;
    const { body } = parseOfxSgml(text);
    const tranList = (body.OFX as Record<string, unknown>).INVTRANLIST as Record<string, unknown>;
    expect(tranList.DTSTART).toBe("20260101");
    expect(Array.isArray(tranList.BUYSTOCK)).toBe(true);
    expect((tranList.BUYSTOCK as unknown[]).length).toBe(2);
    expect(Array.isArray(tranList.SELLSTOCK)).toBe(false);
  });

  it("parses a realistic OFX snippet", () => {
    const text = `OFXHEADER:100
DATA:OFXSGML
VERSION:102
SECURITY:NONE
ENCODING:USASCII

<OFX>
  <SIGNONMSGSRSV1>
    <SONRS>
      <STATUS><CODE>0</CODE><SEVERITY>INFO</SEVERITY></STATUS>
    </SONRS>
  </SIGNONMSGSRSV1>
  <INVSTMTMSGSRSV1>
    <INVSTMTTRNRS>
      <INVSTMTRS>
        <CURDEF>GBP
        <INVACCTFROM>
          <BROKERID>4705</BROKERID>
          <ACCTID>U7033034</ACCTID>
        </INVACCTFROM>
        <INVTRANLIST>
          <DTSTART>20260107202000.000[-5:EST]
          <DTEND>20260403202000.000[-4:EDT]
          <BUYSTOCK>
            <INVBUY>
              <INVTRAN>
                <FITID>123</FITID>
                <DTTRADE>20260303151138.000[-5:EST]
              </INVTRAN>
              <SECID>
                <UNIQUEID>023135106</UNIQUEID>
                <UNIQUEIDTYPE>CUSIP
              </SECID>
              <UNITS>20
              <UNITPRICE>156.55
              <TOTAL>-3131.79
              <CURRENCY>
                <CURRATE>0.7487
                <CURSYM>USD
              </CURRENCY>
            </INVBUY>
            <BUYTYPE>BUY
          </BUYSTOCK>
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
</OFX>`;
    const { header, body } = parseOfxSgml(text);
    expect(header.VERSION).toBe("102");

    const stmtRs = (
      (
        (body.OFX as Record<string, unknown>).INVSTMTMSGSRSV1 as Record<string, unknown>
      ).INVSTMTTRNRS as Record<string, unknown>
    ).INVSTMTRS as Record<string, unknown>;

    expect(stmtRs.CURDEF).toBe("GBP");
    const acct = stmtRs.INVACCTFROM as Record<string, unknown>;
    expect(acct.ACCTID).toBe("U7033034");

    const tranList = stmtRs.INVTRANLIST as Record<string, unknown>;
    expect(tranList.DTSTART).toBe("20260107202000.000[-5:EST]");

    const buy = tranList.BUYSTOCK as Record<string, unknown>;
    const invBuy = buy.INVBUY as Record<string, unknown>;
    expect(invBuy.UNITS).toBe("20");
    expect(invBuy.UNITPRICE).toBe("156.55");
    expect((invBuy.CURRENCY as Record<string, unknown>).CURSYM).toBe("USD");
  });
});
