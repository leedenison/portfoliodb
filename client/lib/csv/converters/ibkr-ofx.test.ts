import { describe, it, expect } from "vitest";
import { convertIbkrOfx } from "./ibkr-ofx";
import { AccountType, AssetClass, IdentifierType, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";
import { expectGroupsBalance } from "@/lib/csv/group-balance.test-utils";

/**
 * A Eurex option trade taken verbatim from
 * local/masters/U1000001_20250107_20260107.qfx, with the OPTINFO that names it
 * and with the broker's own references replaced by invented ones.
 *
 * It is here twice over. It is the arithmetic the contract size has to
 * reproduce: 8 x 20.1105585 x 100 = 16088.4468, and only with the multiplier
 * does that plus the 7.420248 commission reach the -16095.867048 the broker
 * reported. Weighed at one share per contract the group would be short the other
 * 99%. And it is the contract OCC does not list: `P RHM  20250919 560 M` is
 * IBKR's own rendering and no OCC symbol exists for it, however completely the
 * record states the terms.
 */
const EUREX_BUY = `<BUYOPT>
  <INVBUY>
    <INVTRAN><FITID>20250123U10000010000000001</FITID><DTTRADE>20250123030529.000[-5:EST]</DTTRADE></INVTRAN>
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

const EUREX_SEC_LIST = `<OPTINFO><SECINFO>
  <SECID><UNIQUEID>731401093</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
  <SECNAME>P RHM  20250919 560 M RHM 19SEP25 560 P</SECNAME>
  <TICKER>P RHM  20250919 560 M</TICKER>
</SECINFO><OPTTYPE>PUT</OPTTYPE><STRIKEPRICE>560</STRIKEPRICE><DTEXPIRE>20250919</DTEXPIRE><SHPERCTRCT>100</SHPERCTRCT></OPTINFO>`;

/**
 * A US option trade from the same statements, likewise with invented references.
 * The one the file states an OCC symbol for: 3 x 14.127969 x 100 = 4238.3907,
 * which plus 1.57328075 of commission is the -4239.96398075 reported.
 */
const US_BUY = `<BUYOPT>
  <INVBUY>
    <INVTRAN><FITID>20260303U10000010000000002</FITID><DTTRADE>20260303151000.000[-5:EST]</DTTRADE></INVTRAN>
    <SECID><UNIQUEID>786977282</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
    <UNITS>3
    <UNITPRICE>14.127969
    <COMMISSION>1.57328075
    <TAXES>0
    <TOTAL>-4239.96398075
    <CURRENCY><CURRATE>0.7487</CURRATE><CURSYM>USD</CURSYM></CURRENCY>
  </INVBUY>
  <OPTBUYTYPE>BUYTOOPEN
</BUYOPT>`;

/** The record for that contract, with the strike it states left adjustable. */
function usSecList(strike = "470"): string {
  return `<OPTINFO><SECINFO>
  <SECID><UNIQUEID>786977282</UNIQUEID><UNIQUEIDTYPE>CONID</UNIQUEIDTYPE></SECID>
  <SECNAME>BRKB  260918P00470000 BRK B 18SEP26 470 P</SECNAME>
  <TICKER>BRKB  260918P00470000</TICKER>
</SECINFO><OPTTYPE>PUT</OPTTYPE><STRIKEPRICE>${strike}</STRIKEPRICE><DTEXPIRE>20260918</DTEXPIRE><SHPERCTRCT>100</SHPERCTRCT></OPTINFO>`;
}

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
          <DTEND>20260401
          ${transactions}
        </INVTRANLIST>
      </INVSTMTRS>
    </INVSTMTTRNRS>
  </INVSTMTMSGSRSV1>
  <SECLISTMSGSRSV1><SECLIST>${secList}</SECLIST></SECLISTMSGSRSV1>
</OFX>`;
}

/** The option leg of a file holding one option trade. */
function option(ofx: string) {
  return convertIbkrOfx(ofx).postings.find((t) => t.assetClassHint === AssetClass.OPTION)!;
}

describe("convertIbkrOfx", () => {
  it("states the OCC symbol a security record checks out to", () => {
    const result = convertIbkrOfx(buildOfx(US_BUY, usSecList()));
    expect(result.errors).toEqual([]);

    const security = result.postings.find((t) => t.assetClassHint === AssetClass.OPTION)!;
    expect(security.identifierHints).toHaveLength(1);
    expect(security.identifierHints[0]!.type).toBe(IdentifierType.OCC);
    expect(security.identifierHints[0]!.value).toBe("BRKB  260918P00470000");
  });

  it("states no OCC for a contract OCC does not list", () => {
    // The record states every term, so a symbol could be built from them. It is
    // not: what the file prints is not an OCC symbol, and one constructed from a
    // root OCC never issued would name a US-listed contract that is not this one.
    expect(option(buildOfx(EUREX_BUY, EUREX_SEC_LIST)).identifierHints).toHaveLength(0);
  });

  it("refuses a file where the record disagrees with the symbol it prints", () => {
    // The same symbol, against a strike that is not the one it encodes. Both are
    // stated at the one vintage the file carries, so nothing in it says which of
    // the two the file meant -- and it used to state neither and carry on, which
    // left the contract identified by broker text while the faulty file stayed in
    // circulation. See docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
    const result = convertIbkrOfx(buildOfx(US_BUY, usSecList("480")));

    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("identifier_hints");
    // Both symbols named: the reader has to see what disagreed with what.
    expect(result.errors[0]!.message).toContain("BRKB260918P00470000");
    expect(result.errors[0]!.message).toContain("BRKB260918P00480000");

    // And no identifier is stated, as before. A refused file states nothing.
    expect(option(buildOfx(US_BUY, usSecList("480"))).identifierHints).toHaveLength(0);
  });

  it("leaves an option with no SECLIST entry unhinted rather than guessing", () => {
    expect(option(buildOfx(US_BUY, "")).identifierHints).toHaveLength(0);
  });

  it("weighs an option leg by its contract size, so the trade balances", () => {
    // The assertion that matters: expectGroupsBalance mirrors weightOf in
    // balance.go, so a group that balances here is one the server will not route
    // to IMBALANCE. Without the 100x the Eurex trade is out by 15927.56 EUR.
    //
    // Both contracts, because the size follows the asset class the source stated
    // rather than the identifier it managed to state: the Eurex option carries no
    // OCC symbol and is an option all the same.
    expectGroupsBalance(convertIbkrOfx(buildOfx(EUREX_BUY, EUREX_SEC_LIST)).postings);
    expectGroupsBalance(convertIbkrOfx(buildOfx(US_BUY, usSecList())).postings);
  });

  it("splits the commission out of the option's netted total", () => {
    const result = convertIbkrOfx(buildOfx(EUREX_BUY, EUREX_SEC_LIST));

    const cash = result.postings.find((t) => mustBe(t.brokerTxType, TxType.TRADE_CASH))!;
    expect(cash.quantity).toBe("-16088.4468");

    // One posting, in the user's own account. Its expense side is named by the
    // declared type, so the server posts it.
    const fees = result.postings.filter((t) => mustBe(t.brokerTxType, TxType.EXPENSE));
    expect(fees).toHaveLength(1);
    expect(fees[0]!.accountType).toBe(AccountType.USER);
    expect(fees[0]!.quantity).toBe("-7.420248");
  });
});
