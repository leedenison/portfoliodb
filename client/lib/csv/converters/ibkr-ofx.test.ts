import { describe, it, expect } from "vitest";
import { convertIbkrOfx } from "./ibkr-ofx";
import { AccountType, IdentifierType, TxType } from "@/gen/type/v1/type_pb";
import { expectGroupsBalance } from "@/lib/csv/group-balance.test-utils";

/**
 * An option trade taken verbatim from local/masters/U1000001_20250107_20260107.qfx,
 * with the OPTINFO that names it. It is here because it is the arithmetic the
 * contract size has to reproduce: 8 x 20.1105585 x 100 = 16088.4468, and only
 * with the multiplier does that plus the 7.420248 commission reach the
 * -16095.867048 the broker reported. Weighed at one share per contract the
 * group would be short the other 99%.
 */
const OPT_BUY = `<BUYOPT>
  <INVBUY>
    <INVTRAN><FITID>20250123U10000017171218488</FITID><DTTRADE>20250123030529.000[-5:EST]</DTTRADE></INVTRAN>
    <SECID><UNIQUEID>731401093</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
    <UNITS>8
    <UNITPRICE>20.1105585
    <COMMISSION>7.420248
    <TAXES>0
    <TOTAL>-16095.867048
    <CURRENCY><CURRATE>0.8432</CURRATE><CURSYM>EUR</CURSYM></CURRENCY>
  </INVBUY>
  <OPTBUYTYPE>BUYTOCLOSE
</BUYOPT>`;

const OPT_SEC_LIST = `<OPTINFO><SECINFO>
  <SECID><UNIQUEID>731401093</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
  <SECNAME>P RHM  20250919 560 M RHM 19SEP25 560 P</SECNAME>
  <TICKER>P RHM  20250919 560 M</TICKER>
</SECINFO><OPTTYPE>PUT</OPTTYPE><STRIKEPRICE>560</STRIKEPRICE><SHPERCTRCT>100</SHPERCTRCT></OPTINFO>`;

function buildOfx(transactions: string, secList: string): string {
  return `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX>
  <SIGNONMSGSRSV1><SONRS><STATUS><CODE>0</CODE></STATUS></SONRS></SIGNONMSGSRSV1>
  <INVSTMTMSGSRSV1>
    <INVSTMTTRNRS>
      <INVSTMTRS>
        <CURDEF>GBP
        <INVACCTFROM><BROKERID>4705</BROKERID><ACCTID>U1000001</ACCTID></INVACCTFROM>
        <INVTRANLIST>
          <DTSTART>20250101
          <DTEND>20250401
          ${transactions}
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
  <SECLISTMSGSRSV1><SECLIST>${secList}</SECLIST></SECLISTMSGSRSV1>
</OFX>`;
}

describe("convertIbkrOfx", () => {
  it("resolves an option's CONID to its OCC symbol through the SECLIST", () => {
    const result = convertIbkrOfx(buildOfx(OPT_BUY, OPT_SEC_LIST));
    expect(result.errors).toEqual([]);

    const security = result.postings.find((t) => t.type === TxType.BUYOPT)!;
    expect(security.identifierHints).toHaveLength(1);
    expect(security.identifierHints[0]!.type).toBe(IdentifierType.OCC);
    expect(security.identifierHints[0]!.value).toBe("P RHM  20250919 560 M");
  });

  it("weighs the option leg by its contract size, so the trade balances", () => {
    // The assertion that matters: expectGroupsBalance mirrors weightOf in
    // balance.go, so a group that balances here is one the server will not route
    // to IMBALANCE. Without the 100x it is out by 15927.56 EUR.
    expectGroupsBalance(convertIbkrOfx(buildOfx(OPT_BUY, OPT_SEC_LIST)).postings);
  });

  it("splits the commission out of the option's netted total", () => {
    const result = convertIbkrOfx(buildOfx(OPT_BUY, OPT_SEC_LIST));

    const cash = result.postings.find((t) => t.type === TxType.CASHFLOW)!;
    expect(cash.quantity).toBe("-16088.4468");

    const fees = result.postings.filter((t) => t.type === TxType.INVEXPENSE);
    expect(fees).toHaveLength(2);
    expect(fees.find((t) => t.accountType === AccountType.USER)!.quantity).toBe("-7.420248");
    expect(fees.find((t) => t.accountType === AccountType.EXPENSE)!.quantity).toBe("7.420248");
  });

  it("leaves an option with no SECLIST entry unhinted rather than guessing", () => {
    const security = convertIbkrOfx(buildOfx(OPT_BUY, "")).postings.find(
      (t) => t.type === TxType.BUYOPT,
    )!;
    expect(security.identifierHints).toHaveLength(0);
  });
});
