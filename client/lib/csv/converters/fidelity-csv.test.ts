import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Big } from "@/lib/decimal";
import { describe, it, expect } from "vitest";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";
import { convertFidelityToStandard, FIDELITY_TYPE_TO_TYPES } from "./fidelity-csv";
import { expectGroupsBalance, residuals } from "@/lib/csv/group-balance.test-utils";
import { RECORD_LABEL } from "@/lib/csv/postings";

// The header the Fidelity.co.uk download writes, verbatim, trailing comma and
// all: the export writes an empty thirteenth cell after Status. Every test uses
// it, because this converter reads that file and no other. A file with extra
// columns of someone's own is a different file and is not what gets uploaded.
const HEADER =
  "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,";

const convertRows = (rows: string[]) =>
  convertFidelityToStandard([HEADER, ...rows].join("\n"), { currency: "GBP" });

describe("convertFidelityToStandard", () => {
  it("returns error when currency is missing", () => {
    const result = convertFidelityToStandard(HEADER + "\n", {});
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors.some((e) => e.message.includes("Currency"))).toBe(true);
    expect(result.postings.length).toBe(0);
  });

  it("returns error when file is empty", () => {
    const result = convertFidelityToStandard("", { currency: "GBP" });
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.postings.length).toBe(0);
  });

  it("returns error when Fidelity header not found", () => {
    const result = convertFidelityToStandard("foo,bar\n1,2", { currency: "GBP" });
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors.some((e) => e.message.includes("Order date"))).toBe(true);
    expect(result.postings.length).toBe(0);
  });

  // The download opens with five lines of account metadata and a blank line
  // before the header, so finding the header is part of reading the file.
  it("skips the export's metadata preamble", () => {
    const csv = [
      "Account ,All Accounts",
      "Timeframe,04/08/2024-04/08/2025",
      "Transaction type,All Transactions",
      "Investment name,All Investments",
      'Valuations,"£923,821.32"',
      "",
      HEADER,
      '16 Jul 2025,21 Jul 2025,Cash Interest,"Cash",Investment Account,AG10000001,"Cash",0.60,0.6,1,1140097065,Completed,',
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0]!.quantity).toBe("0.6");
  });

  // Converting on whatever columns turned up is how an upload silently comes out
  // thinner than the file it came from, so a short header is refused by name.
  it("names the columns a file is missing rather than converting without them", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "21 Jan 2026,23 Jan 2026,Buy,ISHARES II PLC INRG,SIPP,100,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.postings).toEqual([]);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("header");
    expect(result.errors[0]!.message).toContain("Product Wrapper");
    expect(result.errors[0]!.message).toContain("Amount");
    expect(result.errors[0]!.message).toContain("Status");
  });

  it("parses a single Sell row and carries both of its dates", () => {
    const result = convertRows([
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10000001,,-31826.24,70,454.66,1107095237,Completed,',
    ]);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.postings[0]!.instrumentDescription).toContain("INVESCO");
    expect(result.postings[0]!.quantity).toBe("-70");
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.settlementCurrency).toBe("GBP");
    expect(result.postings[0]!.tradingCurrency).toBeUndefined(); // Sell is not Cash type
    expect(result.postings[0]!.account).toBe("AG10000001");
    // The two columns are two different facts and both survive: Order date is
    // when it was ordered, Completion date when it settled two days later.
    expect(timestampDate(result.postings[0]!.orderDate!).getDate()).toBe(21);
    expect(timestampDate(result.postings[0]!.tradeDate!).getDate()).toBe(23);
    // The window is over the order date, which is what the replace period is
    // matched against.
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBe(0); // Jan
    expect(result.periodFrom.getDate()).toBe(21);
  });

  it("falls back to Order date when Completion date is Pending", () => {
    const result = convertRows([
      "21 Jan 2026,Pending,Buy,ISHARES II PLC INRG,SIPP - Pension Savings Account,AP10000001,,-716.00,100,7.16,1000000001,Completed,",
    ]);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBe(0); // Jan
    expect(result.periodFrom.getDate()).toBe(21);
  });

  // The download writes "21 Jan 2026" in both date columns. A file that is ISO in
  // either of them is a pre-processed file rather than a download.
  it("rejects a row whose dates are not in the broker's format", () => {
    const result = convertRows([
      "2026-01-21,2026-01-23,Buy,ISHARES II PLC INRG,SIPP - Pension Savings Account,AP10000001,,-716.00,100,7.16,1000000001,Completed,",
    ]);
    expect(result.postings).toEqual([]);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("Order date");
  });

  it("parses Cash Interest as INTEREST", () => {
    const result = convertFidelityToStandard(
      [
        HEADER,
        '16 Feb 2026,23 Feb 2026,Cash Interest,"Cash",SIPP - Pension Savings Account,AP10000001,"Pension Cash",3.27,3.27,1,1000000002,Completed,',
      ].join("\n"),
      { currency: "USD" }
    );
    expect(result.errors).toEqual([]);
    // The cash row alone. The income it came from is named by the declared type,
    // so the server posts it.
    expect(result.postings.length).toBe(1);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.INTEREST]);
    expect(result.postings[0]!.quantity).toBe("3.27");
    expect(result.postings[0]!.settlementCurrency).toBe("USD");
  });

  // The exported map is the set of types the converter accepts. Consumers that
  // upload despite dropped rows use it to name the types they dropped, so a type
  // present in the map must not be rejected by the converter.
  it("accepts every transaction type in FIDELITY_TYPE_TO_TYPES", () => {
    const types = Object.keys(FIDELITY_TYPE_TO_TYPES);
    const result = convertRows(
      types.map(
        (t, i) =>
          `20 Oct 2025,22 Oct 2025,${t},ISHARES II PLC INRG,SIPP - Pension Savings Account,AP10000001,,7.16,1,7.16,${2000000000 + i},Completed,`
      )
    );
    expect(result.errors).toEqual([]);
    // A reinvestment's income leg is appended after the rows, so count the postings
    // that came from a source row: those are the ones a type maps to, in row order,
    // each carrying its declared set.
    const source = result.postings.filter((tx) => tx.accountType === AccountType.UNSPECIFIED);
    expect(source.length).toBe(types.length);
    source.forEach((tx, i) => {
      expect(tx.brokerTxType).toEqual(FIDELITY_TYPE_TO_TYPES[types[i]!]);
    });
  });

  it("names the offending type when a row's type is unrecognised", () => {
    const result = convertRows([
      "20 Oct 2025,22 Oct 2025,Corporate Action Reinvestment,ISHARES II PLC INRG,SIPP - Pension Savings Account,AP10000001,,7.16,1,7.16,2000000001,Completed,",
    ]);
    expect(result.postings).toEqual([]);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("type");
    expect(result.errors[0]!.message).toContain("Corporate Action Reinvestment");
  });

  describe("cash movement direction", () => {
    // Taken verbatim from a real export. The Amount column carries the direction
    // of a cash movement; Quantity is an unsigned magnitude.
    const cases: { name: string; row: string; want: string }[] = [
      {
        name: "a fee is an outflow",
        row: '03 Jul 2026,08 Jul 2026,Service Fee,"Cash",Cash Management Account,AW10000001,,-5.20,5.2,1,1192150743,Completed,',
        want: "-5.2",
      },
      {
        name: "interest is an inflow",
        row: '15 Jul 2026,21 Jul 2026,Cash Interest,"Cash",Investment ISA,AS10000001,"Cash",1.26,1.26,1,1197099383,Completed,',
        want: "1.26",
      },
      {
        name: "a transfer out is an outflow",
        row: '03 Jul 2026,03 Jul 2026,Transfer To Cash Management Account For Fees,"Cash",Investment ISA,AS10000001,,-2.30,2.3,1,1191214573,Completed,',
        want: "-2.3",
      },
      {
        name: "the matching transfer in is an inflow",
        row: '03 Jul 2026,03 Jul 2026,Cash In Ring-fenced For Fees,"Cash",Cash Management Account,AW10000001,,2.30,2.3,1,1191214577,Completed,',
        want: "2.3",
      },
      {
        name: "Quantity of zero does not lose the amount",
        row: '15 Jul 2026,21 Jul 2026,Tax On Interest,"Cash",Investment Account,AG10000001,,-0.20,0,0,145848690,Completed,',
        want: "-0.2",
      },
    ];

    for (const tc of cases) {
      it(tc.name, () => {
        const result = convertRows([tc.row]);
        expect(result.errors).toEqual([]);
        expect(result.postings[0]!.quantity).toBe(tc.want);
      });
    }

    it("nets a matched transfer pair to zero", () => {
      // These two rows are the same money leaving one wrapper and arriving in
      // another. Reading the unsigned Quantity made them sum to twice the value.
      const result = convertRows([cases[2]!.row, cases[3]!.row]);
      // Summed in decimal so the assertion holds for any pair rather than for
      // ones whose float64 sum happens to land on zero.
      const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
      expect(total.eq(0)).toBe(true);
    });

    // A row whose Amount the export left blank still has its unsigned Quantity,
    // which is better than posting nothing for it.
    it("falls back to Quantity when a row states no Amount", () => {
      const result = convertRows([
        '15 Jul 2026,21 Jul 2026,Cash Interest,"Cash",Investment ISA,AS10000001,"Cash",,1.26,1,1197099383,Completed,',
      ]);
      expect(result.postings[0]!.quantity).toBe("1.26");
    });
  });

  // Both appeared in a 19-month export and were absent from the type map, so
  // every one of those rows was dropped -- and under replace semantics, dropped
  // means deleted from the period rather than merely skipped.
  it.each(["Cash In For Transfer", "Cash Out For Buy From Transfer"])(
    "recognises %s",
    (type) => {
      const result = convertRows([
        `10 Apr 2025,14 Apr 2025,${type},"Cash",Investment ISA,AS10000001,,-100.00,100,1,123,Completed,`,
      ]);
      expect(result.errors).toEqual([]);
      expect(result.postings).toHaveLength(1);
      expect(result.postings[0]!.quantity).toBe("-100");
    }
  );

  it("keeps share counts for security rows, negating sells", () => {
    const result = convertRows([
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10000001,,-31826.24,70,454.66,1107095237,Completed,',
    ]);
    // Quantity is a share count here, not money, so Amount must not replace it.
    expect(result.postings[0]!.quantity).toBe("-70");
  });

  // Fidelity reports money leaving an account as a sale of the account's cash.
  // Mapping on the transaction type alone made each one a security position sold
  // in units of money, and left the account's balance short by the amount that
  // moved. Rows taken from a real Fidelity export, 2023-03-01.
  describe("a Buy or Sell of cash", () => {
    const TRANSFER =
      "01 Mar 2023,01 Mar 2023,Transfer To Cash Management Account,Cash,Investment Account,AG10000001,,-401,401,1,608442557,Completed,";
    const SELL =
      "01 Mar 2023,01 Mar 2023,Sell,Cash,Investment Account,AG10000001,,-401,401,1,608442561,Completed,";
    const CASH_IN =
      "01 Mar 2023,01 Mar 2023,Cash In From Sell,Cash,Investment Account,AG10000001,,401,401,1,608442563,Completed,";

    it("is a cash movement, not a security trade", () => {
      const result = convertRows([SELL]);
      expect(result.errors).toEqual([]);
      expect(result.postings).toHaveLength(1);
      expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
      expect(result.postings[0]!.assetClassHint).toBe(AssetClass.CASH);
      // The signed Amount, not the unsigned share count the row also carries.
      expect(result.postings[0]!.quantity).toBe("-401");
      expect(result.postings[0]!.tradingCurrency).toBe("GBP");
    });

    it("groups with its cash in, and the pair weighs nothing", () => {
      expectGroupsBalance(convertRows([SELL, CASH_IN]).postings);
    });

    it("leaves the transfer beside it as the money that moved", () => {
      // All three rows are the same 401. Only the transfer is a movement: the
      // other two are the broker converting its own cash position, and net to
      // zero. The account must end 401 down, matching the credit the cash
      // management account records against it.
      const result = convertRows([TRANSFER, SELL, CASH_IN]);
      expect(result.errors).toEqual([]);
      const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
      expect(total.eq(-401)).toBe(true);
      expect(result.postings.some((tx) => mustBe(tx.brokerTxType, TxType.TRADE_ASSET))).toBe(false);
    });

    it("still reads a security sale as one", () => {
      const result = convertRows([
        '08 Feb 2022,10 Feb 2022,Sell,"WISE PLC, CLS A ORD GBP0.01 (WISE)",SIPP - Pension Savings Account,AP10000001,,-7266.49,1242,5.85,441416452,Completed,',
      ]);
      expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
      expect(result.postings[0]!.quantity).toBe("-1242");
    });
  });

  it("parses Buy with positive quantity", () => {
    const result = convertRows([
      "20 Oct 2025,22 Oct 2025,Buy,ISHARES II PLC INRG,SIPP - Pension Savings Account,AP10000001,,-91526.28,12783,7.16,1000000003,Completed,",
    ]);
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.quantity).toBe("12783");
    expect(result.postings[0]!.settlementCurrency).toBe("GBP");
    expect(result.postings[0]!.tradingCurrency).toBeUndefined(); // Buy is not Cash type
  });
});

describe("trades the broker names for their reason", () => {
  const convert = convertRows;

  it("pairs a dividend reinvestment with the cash out that funded it", () => {
    const result = convert([
      '11 Feb 2022,15 Feb 2022,Buy From Dividend,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",21.75,17,1.19,442184379,Completed,',
      '11 Feb 2022,15 Feb 2022,Cash Out For Dividend Reinvestment,Cash,Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",-20.15,20.15,1,442184374,Completed,',
    ]);

    expect(result.errors).toEqual([]);
    // A plain purchase: the dividend has already posted as its own Cash Dividend
    // row, so deriving a reinvestment income leg here would count it twice.
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.quantity).toBe("17");
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
  });

  it("pairs a rebate reinvestment with its cash out", () => {
    const result = convert([
      "04 Mar 2022,10 Mar 2022,Cash Out,Cash,SIPP - Pension Savings Account,AP10000002,M&G European Index Tracker,-7.81,7.81,1,452602191,Completed,",
      "04 Mar 2022,10 Mar 2022,Buy From Rebate,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,M&G European Index Tracker,7.81,9.24,0.85,452602204,Completed,",
    ]);

    expect(result.errors).toEqual([]);
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    // The group does not weigh zero, and cannot: 9.24 units at the printed price
    // of 0.85 is 7.854 against a cash out of 7.81. The price is rounded to the
    // penny and the units are not, so the 0.044 is the source disagreeing with
    // itself. The imbalance report is where that belongs.
    expect(residuals(result.postings)).toEqual({ GBP: "0.044" });
  });

  it("derives the income a reinvestment consumed, since no row reports it", () => {
    // The one trade in either export with no cash row anywhere beside it: the
    // income buys the units without arriving as money first.
    const result = convert([
      "24 Mar 2022,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,Baillie Gifford Responsible Global Equity Income B Inc,31.65,21.09,1.5,460143202,Completed,",
    ]);

    expect(result.errors).toEqual([]);
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.quantity).toBe("21.09");
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.DIVIDEND]);
    expect(result.postings[1]!.accountType).toBe(AccountType.INCOME);
    // The posting's own weight, not the 31.65 the broker printed: taking that
    // would leave the group short by the rounding in the quoted price.
    expect(result.postings[1]!.quantity).toBe("-31.635");
    expect(result.postings[1]!.instrumentDescription).toBe("GBP");
    // Both legs were read out of one row, so both carry that row's reference and
    // the pair can be put back together from evidence rather than from the ref.
    expect(result.postings[1]!.correlations).toEqual(result.postings[0]!.correlations);
    expect(result.postings[1]!.correlations[0]!.token).toBe("460143202");
    expectGroupsBalance(result.postings);
  });

  // Nothing in either export reaches this, but a row the source referenced by
  // nothing still has to hold its own legs together once the ref is gone.
  it("identifies a reinvestment the export gave no reference", () => {
    const result = convert([
      "24 Mar 2022,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,Baillie Gifford Responsible Global Equity Income B Inc,31.65,21.09,1.5,,Completed,",
    ]);

    expect(result.errors).toEqual([]);
    expect(result.postings).toHaveLength(2);
    const c = result.postings[0]!.correlations[0]!;
    expect(c.label).toBe(RECORD_LABEL);
    expect(c.scope).toBe(Scope.FILE);
    expect(result.postings[1]!.correlations[0]!.token).toBe(c.token);
    expectGroupsBalance(result.postings);
  });

  it("settles a switch through the rows the broker used for it", () => {
    const result = convert([
      "28 Jun 2023,29 Jun 2023,Cash Out,Cash,SIPP - Pension Savings Account,AP10000002,,-12091.15,12091.15,1,661638572,Completed,",
      "28 Jun 2023,04 Jul 2023,Sell For Switch,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,,-12091.15,12147.03,1,661638570,Completed,",
      "28 Jun 2023,29 Jun 2023,Buy For Switch,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638575,Completed,",
      "28 Jun 2023,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,",
    ]);

    expect(result.errors).toEqual([]);
    // The buy side names cash as the asset, so it is a movement of money rather
    // than a purchase of a security called Cash, and it nets against its cash out.
    expect(result.postings[2]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
    // The sale side is a real disposal, and its proceeds arrive as a Cash In.
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
  });

  it("keeps a Cash In's declared set whether or not it paired with a sale", () => {
    // Cash In is money from outside the account or a sale's proceeds, and no
    // lookup on the broker's name can tell the two apart. The declared set says
    // exactly that, and pairing records itself in the group alone rather than
    // rewriting what the broker declared.
    const paired = convert([
      "28 Jun 2023,04 Jul 2023,Sell For Switch,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,,-12091.15,12147.03,1,661638570,Completed,",
      "28 Jun 2023,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,",
    ]);
    expect(paired.postings[1]!.brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
    expect(paired.postings[1]!.tradingCurrency).toBe("GBP");

    const alone = convert([
      "28 Jun 2023,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,",
    ]);
    expect(alone.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
  });

  it("skips a cancelled transaction and its cancelled cash row", () => {
    const result = convert([
      '22 Jul 2024,22 Jul 2024,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ (EQQQ)",Investment Account,AG10000001,,0,0,373.51,847633602,Cancelled,',
      "22 Jul 2024,22 Jul 2024,Cash In From Sell,Cash,Investment Account,AG10000001,,0,0,1,847633604,Cancelled,",
      "22 Jul 2024,22 Jul 2024,Dealing Fee,Cash,Investment Account,AG10000001,,-7.5,0,0,100000000,Completed,",
    ]);

    expect(result.errors).toEqual([]);
    // The fee alone; nothing from the trade that never happened.
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRANSACTION_COST]);
  });
});

// The export says "Cash" in the same column it names a security in, so reading it
// through described money as an instrument. A posting is described by, and carries
// the identifier of, whatever resolves it. See docs/spec/archive-format.md.
describe("what a posting resolves to", () => {
  const convert = convertRows;

  it("describes a cash posting by its currency and hints at it", () => {
    const result = convert([
      "11 Jan 2022,11 Jan 2022,Service Fee,Cash,Cash Management Account,AW10000001,,-3.24,3.24,1,428845305,Completed,",
    ]);

    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
    expect(result.postings[0]!.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
  });

  it("describes a dividend by the currency it paid, not the payer", () => {
    // Source investment names the security that paid it. Describing the posting
    // by the payer would resolve the money into that holding; carrying the payer
    // elsewhere is 0049's to decide.
    const result = convert([
      '11 Feb 2022,11 Feb 2022,Cash Dividend,Cash,Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",22.67,22.67,1,442183607,Completed,',
    ]);

    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.DIVIDEND]);
  });

  // The export gives a security no ISIN, no SEDOL and no exchange; the ticker in
  // the trailing parentheses of its description is the whole of what it offers.
  it("keeps a security's own description and offers the ticker it ends with", () => {
    const result = convert([
      '08 Feb 2022,10 Feb 2022,Sell,"WISE PLC, CLS A ORD GBP0.01 (WISE)",SIPP - Pension Savings Account,AP10000001,,-7266.49,1242,5.85,441416452,Completed,',
    ]);

    expect(result.postings[0]!.instrumentDescription).toContain("WISE PLC");
    // No domain: the export names no venue, and a ticker under a guessed one is a
    // claim the source never made.
    expect(result.postings[0]!.identifierHints.map((h) => [h.type, h.value, h.domain])).toEqual([
      [IdentifierType.MIC_TICKER, "WISE", ""],
    ]);
  });

  it("takes the last parenthetical, not one from inside the description", () => {
    // Fidelity writes the share class and the quote currency in parentheses of
    // their own. Reading the first would offer GBP as a ticker.
    const result = convert([
      '10 Apr 2025,14 Apr 2025,Buy,"ISHARES PHYSICAL METALS PLC, ISHARES PHYSICAL GOLD ETC USD (GBP) ACC (SGLN)",Investment ISA,AS10000001,,-5000.00,100,50,1000000004,Completed,',
    ]);

    expect(result.postings[0]!.identifierHints.map((h) => h.value)).toEqual(["SGLN"]);
  });

  it("keeps the trailing dot an LSE ticker can carry", () => {
    const result = convert([
      '09 Apr 2025,14 Apr 2025,Sell,"BAE SYSTEMS, ORD GBP0.025 (BA.)",Investment ISA,AS10000001,,-32991.25,2000,16.5,1000000005,Completed,',
    ]);

    expect(result.postings[0]!.identifierHints.map((h) => h.value)).toEqual(["BA."]);
  });

  it("offers no ticker for a fund, which ends in none", () => {
    // An unlisted fund is written bare -- no parentheses anywhere. Offering its
    // name as a symbol would resolve to nothing and leave the name in the
    // security master twice, so the row goes on its description alone.
    const result = convert([
      "24 Mar 2022,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,,31.65,21.09,1.5,460143202,Completed,",
    ]);

    expect(result.postings[0]!.identifierHints).toEqual([]);
    expect(result.postings[0]!.instrumentDescription).toContain("Baillie Gifford");
  });

  // The export states no asset class. A ticker says the security is listed, which
  // is not the same claim, so nothing is asserted and the plugins decide.
  it("states no asset class for a security row", () => {
    const result = convert([
      '08 Feb 2022,10 Feb 2022,Sell,"WISE PLC, CLS A ORD GBP0.01 (WISE)",SIPP - Pension Savings Account,AP10000001,,-7266.49,1242,5.85,441416452,Completed,',
    ]);

    expect(result.postings[0]!.assetClassHint).toBe(AssetClass.ASSET_CLASS_UNSPECIFIED);
  });
});

// The other side of a one-sided cash row is the server's now, so what these check is
// that the converter emits the row and leaves the boundary leg to residual.Boundary --
// and that what it emits is enough for the server to name one.
describe("one-sided cash rows", () => {
  const row = (
    type: string,
    investments: string,
    account: string,
    amount: string,
    quantity: string,
    price: string,
    ref: string,
    completion = "10 Feb 2022"
  ) =>
    `08 Feb 2022,${completion},${type},"${investments}",Investment Account,${account},,${amount},${quantity},${price},${ref},Completed,`;

  it("emits a charge as one posting, declared so the server can place its expense", () => {
    const result = convertRows([row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "441416483")]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].brokerTxType).toEqual([TxType.TRANSACTION_COST]);
    expect(result.postings[0].accountType).toBe(AccountType.UNSPECIFIED);
    expect(result.postings[0].quantity).toBe("-10");
    // An expense under every reading, which is what entitles the server to name the
    // account the money went to.
    expect(mustBe(result.postings[0].brokerTxType, TxType.EXPENSE)).toBe(true);
    expectGroupsBalance(result.postings);
  });

  it("emits a dividend as one posting, declared so the server can place its income", () => {
    const result = convertRows([row("Cash Dividend", "Cash", "AG1", "23.40", "23.40", "1", "441416484")]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].quantity).toBe("23.4");
    expect(mustBe(result.postings[0].brokerTxType, TxType.INCOME)).toBe(true);
    expectGroupsBalance(result.postings);
  });

  it("leaves a trade and its cash leg alone -- the source supplied both", () => {
    const result = convertRows([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "441416454"),
    ]);

    expect(result.postings).toHaveLength(2);
    expectGroupsBalance(result.postings);
  });

  it("balances a trade, its cash leg and the charge reported beside it", () => {
    const result = convertRows([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "441416454"),
      row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "441416483", "08 Feb 2022"),
    ]);

    expect(result.postings).toHaveLength(3);
    expectGroupsBalance(result.postings);
  });

  it("does not invent a leg for a transfer, whose other side is another account", () => {
    const result = convertRows([row("Cash In", "Cash", "AG1", "5000", "5000", "1", "441416485")]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
  });
});

describe("trade cash legs", () => {
  // Typing these transfers made every trade group read as one, so its residual
  // was routed to TRANSFER_CLEARING instead of IMBALANCE.
  it("are TRADE_CASH, keeping their direction and currency", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash Out For Buy,Cash,Investment Account,AG1,,-401,401,1,608443430,Completed,",
      "08 Feb 2022,10 Feb 2022,Cash In From Sell,Cash,Investment Account,AG1,,7265.70,7265.70,1,441416454,Completed,",
    ]);

    expect(result.postings[0].brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(result.postings[0].quantity).toBe("-401");
    expect(result.postings[0].tradingCurrency).toBe("GBP");
    expect(result.postings[1].brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(result.postings[1].quantity).toBe("7265.7");
  });
});

// What the source said about why a row might belong with another one. A Fidelity
// reference number is honestly comparable both ways -- two rows of one event are
// numbered near each other, and one row is named by exactly one reference -- so
// the correlation declares both and carries the number in a field of its own.
describe("correlations", () => {
  // The two sides of one transfer hop, verbatim from the master export. Their
  // references differ by 3, which is what tells this month's fee transfer from
  // last month's when the amounts are identical -- and what the ordinal is for.
  it("states what a reference number is comparable by, on every transcribed row", () => {
    const result = convertRows([
      "06 May 2022,06 May 2022,Transfer To Cash Management Account For Fees,Cash,SIPP,AP1,,-2.19,2.19,1,481052149,Completed,",
      "06 May 2022,06 May 2022,Cash In Ring-fenced For Fees,Cash,Cash Management Account,AW1,,2.19,2.19,1,481052152,Completed,",
    ]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[1].correlations[0]!.ordinal).toBe(481052152n);
    expect(result.postings[0].correlations).toHaveLength(1);
    const c = result.postings[0].correlations[0]!;
    expect(c.label).toBe("");
    expect(c.token).toBe("481052149");
    expect(c.ordinal).toBe(481052149n);
    expect(c.scope).toBe(Scope.FILE);
    expect(c.match).toEqual([Match.EXACT, Match.ORDINAL]);
    // The span the deposit rule is measured against travels with the evidence,
    // because how densely Fidelity issues references is a fact about its
    // numbering rather than a grouping policy.
    expect(c.ordinalSpan).toBe(8n);
  });

  // Nothing in the sample exports writes one, but the field is opaque and a
  // reference in some other shape has to keep its equality half rather than
  // become NaN.
  it("keeps equality alone for a reference that carries no number", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash Interest,Cash,Investment Account,AG1,,1.18,1.18,1,REF/2022/A,Completed,",
    ]);

    const c = result.postings[0].correlations[0]!;
    expect(c.token).toBe("REF/2022/A");
    expect(c.ordinal).toBeUndefined();
    expect(c.ordinalSpan).toBeUndefined();
    expect(c.match).toEqual([Match.EXACT]);
  });

  it("correlates nothing for a row the source gave no reference for", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash Interest,Cash,Investment Account,AG1,,1.18,1.18,1,,Completed,",
    ]);

    expect(result.postings[0].correlations).toEqual([]);
  });

  // A derived leg transcribes nothing, so it correlates with nothing. Copying
  // the correlation of the posting it mirrors would state that the source
  // correlated a row it never wrote.
  it("correlates the row the source wrote, and emits nothing else to correlate", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash Dividend,Cash,Investment Account,AG1,,23.40,23.40,1,441416483,Completed,",
    ]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].correlations).toHaveLength(1);
  });
});

describe("the source's own cash total", () => {
  // The number the pairing rules actually match on. It is independent of
  // quantity * unit price, which the export rounds, so the two disagreeing is
  // evidence that two legs do not belong together.
  it("transcribes Amount on a security row, unsigned", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Sell,Legal & General Global Health,ISA,AG1,,20514.62,2676,7.67,795832439,Completed,",
    ]);

    expect(result.postings[0].settlementAmount).toBe("20514.62");
    // Not quantity * unit price, which is 20524.92 here: the whole point is that
    // the two come from different fields.
    expect(result.postings[0].quantity).toBe("-2676");
  });

  it("keeps the magnitude of a purchase, which the export writes negative", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Buy,Legal & General Global Health,ISA,AG1,,-4487.98,585,7.67,795832440,Completed,",
    ]);

    expect(result.postings[0].settlementAmount).toBe("4487.98");
  });

  // A cash row's quantity is already the amount, so stating it again would put
  // the same figure on the posting twice and leave two values to disagree.
  it("states nothing on a cash row", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash In From Sell,Cash,ISA,AG1,,20514.62,20514.62,1,795832441,Completed,",
    ]);

    expect(result.postings[0].settlementAmount).toBeUndefined();
  });

  it("states nothing on a money row, whose quantity is already the total", () => {
    const result = convertRows([
      "08 Feb 2022,10 Feb 2022,Cash Dividend,Cash,Investment Account,AG1,,23.40,23.40,1,441416483,Completed,",
    ]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].quantity).toBe("23.4");
    expect(result.postings[0].settlementAmount).toBeUndefined();
  });
});
