import { Big } from "@/lib/decimal";
import { describe, it, expect } from "vitest";
import { AccountType, AssetClass, IdentifierType, Match, Scope, TxType } from "@/gen/type/v1/type_pb";
import { mustBe } from "@/lib/tx-type";
import { convertFidelityToStandard, FIDELITY_TYPE_TO_TYPES } from "./fidelity-csv";
import { expectGroupsBalance, residuals } from "@/lib/csv/group-balance.test-utils";
import { RECORD_LABEL } from "@/lib/csv/postings";

const HEADER =
  "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit";

describe("convertFidelityToStandard", () => {
  it("returns error when currency is missing", () => {
    const result = convertFidelityToStandard("Order date,Transaction type,Investments\n", {});
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

  it("parses a single Sell row and uses Completion date", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status",
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10000001,,-31826.24,70,454.66,1107095237,Completed,',
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.postings[0]!.instrumentDescription).toContain("INVESCO");
    expect(result.postings[0]!.quantity).toBe("-70");
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.settlementCurrency).toBe("GBP");
    expect(result.postings[0]!.tradingCurrency).toBeUndefined(); // Sell is not Cash type
    expect(result.postings[0]!.account).toBe("AG10000001");
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBe(0); // Jan
    expect(result.periodFrom.getDate()).toBe(23);
  });

  it("falls back to Order date when Completion date is Pending", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "21 Jan 2026,Pending,Buy,ISHARES II PLC INRG,SIPP,100,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBe(0); // Jan
    expect(result.periodFrom.getDate()).toBe(21);
  });

  it("reads an ISO completion date, which some exports carry throughout", () => {
    // The order date is ISO in every export and the completion date usually is
    // not, but one of the two sample exports is ISO in both. Reading only the
    // broker's own format rejected every row of that file.
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "2026-01-21,2026-01-23,Buy,ISHARES II PLC INRG,SIPP,100,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.periodFrom).toEqual(new Date(2026, 0, 23));
  });

  it("parses Cash Interest as INTEREST", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "16 Feb 2026,23 Feb 2026,Cash Interest,Cash,AP10000001,3.27,1",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "USD" });
    expect(result.errors).toEqual([]);
    // The cash row plus the income it came from.
    expect(result.postings.length).toBe(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.INTEREST]);
    expect(result.postings[0]!.quantity).toBe("3.27");
    expect(result.postings[0]!.settlementCurrency).toBe("USD");
  });

  // The exported map is the set of types the converter accepts. Consumers that
  // upload despite dropped rows use it to name the types they dropped, so a type
  // present in the map must not be rejected by the converter.
  it("accepts every transaction type in FIDELITY_TYPE_TO_TYPES", () => {
    const types = Object.keys(FIDELITY_TYPE_TO_TYPES);
    const csv = [
      HEADER,
      ...types.map((t) => `20 Oct 2025,22 Oct 2025,${t},ISHARES II PLC INRG,SIPP,1,7.16`),
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    // Counter-legs and derived income legs are appended for the one-sided rows,
    // so count the postings that came from a source row: those are the ones a
    // type maps to, in row order, each carrying its declared set.
    const source = result.postings.filter((tx) => tx.accountType === AccountType.UNSPECIFIED);
    expect(source.length).toBe(types.length);
    source.forEach((tx, i) => {
      expect(tx.brokerTxType).toEqual(FIDELITY_TYPE_TO_TYPES[types[i]!]);
    });
  });

  it("names the offending type when a row's type is unrecognised", () => {
    const csv = [
      HEADER,
      "20 Oct 2025,22 Oct 2025,Corporate Action Reinvestment,ISHARES II PLC INRG,SIPP,1,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.postings).toEqual([]);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("type");
    expect(result.errors[0]!.message).toContain("Corporate Action Reinvestment");
  });

  // Rows below are taken verbatim from a real export. The full header is needed
  // because the Amount column carries the direction of a cash movement.
  const FULL_HEADER =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,";

  describe("cash movement direction", () => {
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
        const result = convertFidelityToStandard([FULL_HEADER, tc.row].join("\n"), { currency: "GBP" });
        expect(result.errors).toEqual([]);
        expect(result.postings[0]!.quantity).toBe(tc.want);
      });
    }

    it("nets a matched transfer pair to zero", () => {
      // These two rows are the same money leaving one wrapper and arriving in
      // another. Reading the unsigned Quantity made them sum to twice the value.
      const csv = [FULL_HEADER, cases[2]!.row, cases[3]!.row].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      // Summed in decimal so the assertion holds for any pair rather than for
      // ones whose float64 sum happens to land on zero.
      const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
      expect(total.eq(0)).toBe(true);
    });
  });

  // Both appeared in a 19-month export and were absent from the type map, so
  // every one of those rows was dropped -- and under replace semantics, dropped
  // means deleted from the period rather than merely skipped.
  it.each(["Cash In For Transfer", "Cash Out For Buy From Transfer"])(
    "recognises %s",
    (type) => {
      const csv = [
        FULL_HEADER,
        `10 Apr 2025,14 Apr 2025,${type},"Cash",Investment ISA,AS10000001,,-100.00,100,1,123,Completed,`,
      ].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      expect(result.errors).toEqual([]);
      expect(result.postings).toHaveLength(1);
      expect(result.postings[0]!.quantity).toBe("-100");
    }
  );

  it("keeps share counts for security rows, negating sells", () => {
    const csv = [
      FULL_HEADER,
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10000001,,-31826.24,70,454.66,1107095237,Completed,',
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    // Quantity is a share count here, not money, so Amount must not replace it.
    expect(result.postings[0]!.quantity).toBe("-70");
  });

  it("falls back to Quantity when the export has no Amount column", () => {
    const csv = [
      HEADER,
      "15 Jul 2026,21 Jul 2026,Cash Interest,Cash,AS10000001,1.26,1",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.postings[0]!.quantity).toBe("1.26");
  });

  // Fidelity reports money leaving an account as a sale of the account's cash.
  // Mapping on the transaction type alone made each one a security position sold
  // in units of money, and left the account's balance short by the amount that
  // moved. Rows taken from a real Fidelity export, 2023-03-01, with the
  // completion date in the format the broker's own download carries.
  describe("a Buy or Sell of cash", () => {
    const CASH_ASSET_HEADER =
      "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,Exchange,Symbol,Type,Action";
    const TRANSFER =
      "2023-03-01,01 Mar 2023,Transfer To Cash Management Account,Cash,Investment Account,AG10000001,,-401,401,1,608442557,Completed,,GBP,CASH,Cash";
    const SELL =
      "2023-03-01,01 Mar 2023,Sell,Cash,Investment Account,AG10000001,,-401,401,1,608442561,Completed,,GBP,CASH,Cash";
    const CASH_IN =
      "2023-03-01,01 Mar 2023,Cash In From Sell,Cash,Investment Account,AG10000001,,401,401,1,608442563,Completed,,GBP,CASH,Cash";

    const convert = (rows: string[]) =>
      convertFidelityToStandard([CASH_ASSET_HEADER, ...rows].join("\n"), { currency: "GBP" });

    it("is a cash movement, not a security trade", () => {
      const result = convert([SELL]);
      expect(result.errors).toEqual([]);
      expect(result.postings).toHaveLength(1);
      expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
      expect(result.postings[0]!.assetClassHint).toBe(AssetClass.CASH);
      // The signed Amount, not the unsigned share count the row also carries.
      expect(result.postings[0]!.quantity).toBe("-401");
      expect(result.postings[0]!.tradingCurrency).toBe("GBP");
    });

    it("groups with its cash in, and the pair weighs nothing", () => {
      const result = convert([SELL, CASH_IN]);
      expect(result.postings[0]!.groupRef).toBe("608442561");
      expect(result.postings[1]!.groupRef).toBe("608442561");
      expectGroupsBalance(result.postings);
    });

    it("leaves the transfer beside it as the money that moved", () => {
      // All three rows are the same 401. Only the transfer is a movement: the
      // other two are the broker converting its own cash position, and net to
      // zero. The account must end 401 down, matching the credit the cash
      // management account records against it.
      const result = convert([TRANSFER, SELL, CASH_IN]);
      expect(result.errors).toEqual([]);
      const total = result.postings.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
      expect(total.eq(-401)).toBe(true);
      expect(result.postings.some((tx) => mustBe(tx.brokerTxType, TxType.TRADE_ASSET))).toBe(false);
    });

    it("still reads a security sale as one", () => {
      const result = convert([
        '2022-02-08,10 Feb 2022,Sell,"WISE PLC, CLS A ORD GBP0.01 (WISE)",SIPP - Pension Savings Account,AP10000001,,-7266.49,1242,5.85,441416452,Completed,LON,WISE,STOCK,Sell',
      ]);
      expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
      // The export's own asset column, stated as a hint.
      expect(result.postings[0]!.assetClassHint).toBe(AssetClass.STOCK);
      expect(result.postings[0]!.quantity).toBe("-1242");
    });

    it("does not let a sale of cash claim a security sale's cash in", () => {
      // Both settle in the same account on the same day. Amount separates them,
      // and a security sale picks first regardless of export order.
      const result = convert([
        "2024-04-10,10 Apr 2024,Sell,Cash,Investment Account,AG10000001,,-19999,19999,1,795832439,Completed,,GBP,CASH,Cash",
        "2024-04-10,10 Apr 2024,Cash In From Sell,Cash,Investment Account,AG10000001,,19999,19999,1,795832440,Completed,,GBP,CASH,Cash",
        '2024-04-10,10 Apr 2024,Sell,"VANGUARD FUNDS PLC, S&P 500 (VUSA)",Investment Account,AG10000001,,-19502.15,325,60.0066,791691783,Completed,LON,VUSA,ETF,Sell',
        "2024-04-10,10 Apr 2024,Cash In From Sell,Cash,Investment Account,AG10000001,,19502.15,19502.15,1,791691785,Completed,,GBP,CASH,Cash",
      ]);

      expect(result.postings[0]!.groupRef).toBe("795832439");
      expect(result.postings[1]!.groupRef).toBe("795832439");
      expect(result.postings[2]!.groupRef).toBe("791691783");
      expect(result.postings[3]!.groupRef).toBe("791691783");
    });

    // The map-completeness test above feeds security rows through a header with
    // no Type column, so the fallback has to keep them out of this rule.
    it("falls back to the instrument description when the export omits Type", () => {
      const csv = [
        HEADER,
        "8 Feb 2022,10 Feb 2022,Sell,Cash,AG10000001,401,1",
        "8 Feb 2022,10 Feb 2022,Sell,ISHARES II PLC INRG,AG10000001,100,7.16",
      ].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
      expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    });
  });

  it("parses Buy with positive quantity", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "20 Oct 2025,22 Oct 2025,Buy,ISHARES II PLC INRG,SIPP,12783,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.postings.length).toBe(1);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.quantity).toBe("12783");
    expect(result.postings[0]!.settlementCurrency).toBe("GBP");
    expect(result.postings[0]!.tradingCurrency).toBeUndefined(); // Buy is not Cash type
  });
});

describe("transaction grouping", () => {
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
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
    `8 Feb 2022,${completion},${type},"${investments}",Investment Account,${account},,${amount},${quantity},${price},${ref},Completed`;

  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  it("pairs a sell with the cash in of the same amount", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "441416454"),
    ]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[0].groupRef).toBe("441416452");
    expect(result.postings[1].groupRef).toBe("441416452");
  });

  it("pairs a buy with the cash out despite the fee gap", () => {
    // The buy row's Amount includes a 10.00 dealing fee that Fidelity posts as its
    // own row, so the cash out is 10.00 smaller.
    const result = convert([
      row("Cash Out For Buy", "Cash", "AG1", "-7380.19", "7380.19", "1", "441416514"),
      row("Buy", "INVESCO EQQQ (EQQQ)", "AG1", "7390.19", "28", "263.58", "441416515"),
    ]);

    expect(result.postings[0].groupRef).toBe("441416515");
    expect(result.postings[1].groupRef).toBe("441416515");
  });

  it("keeps a separately reported charge out of the trade's group", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "441416454"),
      row("Dealing Fee", "Cash", "AG1", "-10", "0", "0", "441416483", "8 Feb 2022"),
    ]);

    // It has a group of its own -- it needs one to hold its expense leg -- but
    // Fidelity dates it on the order date while the trade settles later, so
    // folding it into the trade would misdate it.
    expect(result.postings[2].groupRef).toBeTruthy();
    expect(result.postings[2].groupRef).not.toBe(result.postings[0].groupRef);
  });

  it("does not cross-pair two sells settling the same day in one account", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-100.00", "10", "10", "1001"),
      row("Sell", "BP PLC (BP)", "AG1", "-200.00", "20", "10", "1003"),
      row("Cash In From Sell", "Cash", "AG1", "200.00", "200", "1", "1004"),
      row("Cash In From Sell", "Cash", "AG1", "100.00", "100", "1", "1002"),
    ]);

    // Amount, not proximity, decides: the 100.00 cash belongs to the 100.00 sell
    // even though a different cash row sits closer in the file.
    expect(result.postings[0].groupRef).toBe("1001");
    expect(result.postings[3].groupRef).toBe("1001");
    expect(result.postings[1].groupRef).toBe("1003");
    expect(result.postings[2].groupRef).toBe("1003");
  });

  it("does not let one buy strand another by claiming its cash row", () => {
    // The 29939.31 buy sits one reference from the 36946.72 cash out, but a fee
    // cannot be negative, so that cash row can only belong to the larger buy.
    const result = convert([
      row("Cash Out For Buy", "Cash", "AP1", "-29931.81", "29931.81", "1", "697042341"),
      row("Buy", "VANGUARD S&P 500 (VUSA)", "AP1", "29939.31", "100", "299.39", "697042343"),
      row("Cash Out For Buy", "Cash", "AP1", "-36946.72", "36946.72", "1", "697042344"),
      row("Buy", "INVESCO EQQQ (EQQQ)", "AP1", "36954.22", "120", "307.95", "697042346"),
    ]);

    expect(result.postings[1].groupRef).toBe("697042343");
    expect(result.postings[0].groupRef).toBe("697042343");
    expect(result.postings[3].groupRef).toBe("697042346");
    expect(result.postings[2].groupRef).toBe("697042346");
  });

  it("does not pair a sell across accounts or settlement dates", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-100.00", "10", "10", "1001"),
      row("Cash In From Sell", "Cash", "AS2", "100.00", "100", "1", "1002"),
      row("Cash In From Sell", "Cash", "AG1", "100.00", "100", "1", "1003", "11 Feb 2022"),
    ]);

    for (const tx of result.postings) {
      expect(tx.groupRef).toBeUndefined();
    }
  });

  it("does not pair a buy across accounts or settlement dates", () => {
    // Both cash rows would satisfy the fee constraint and sit one reference away,
    // so only the account and settlement date keep them apart.
    const result = convert([
      row("Buy", "INVESCO EQQQ (EQQQ)", "AG1", "107.50", "10", "10", "1002"),
      row("Cash Out For Buy", "Cash", "AS2", "-100.00", "100", "1", "1001"),
      row("Cash Out For Buy", "Cash", "AG1", "-100.00", "100", "1", "1003", "11 Feb 2022"),
    ]);

    for (const tx of result.postings) {
      expect(tx.groupRef).toBeUndefined();
    }
  });

  it("rejects a cash row inconsistent with quantity times unit price", () => {
    // Both cash rows satisfy the fee rule for both buys, and the wrong ones sit
    // closer in reference order. Only quantity * unit price separates them: the
    // 100-share buy at 300 cannot have cost 8,000.
    const result = convert([
      row("Cash Out For Buy", "Cash", "AP1", "-8000.00", "8000", "1", "2001"),
      row("Buy", "VANGUARD S&P 500 (VUSA)", "AP1", "30007.50", "100", "300", "2002"),
      row("Cash Out For Buy", "Cash", "AP1", "-30000.00", "30000", "1", "2003"),
      row("Buy", "INVESCO EQQQ (EQQQ)", "AP1", "8007.50", "20", "400", "2004"),
    ]);

    expect(result.postings[1].groupRef).toBe("2002");
    expect(result.postings[2].groupRef).toBe("2002");
    expect(result.postings[3].groupRef).toBe("2004");
    expect(result.postings[0].groupRef).toBe("2004");
  });

  it("tolerates the rounding in a quoted unit price", () => {
    // 2676 * 7.67 is 20,524.92 while the broker settled 20,514.62 -- 0.05% out,
    // because the export rounds the price. A tighter check would reject real trades.
    const result = convert([
      row("Sell", "ISHARES INRG (INRG)", "AG1", "-20514.62", "2676", "7.67", "3001"),
      row("Cash In From Sell", "Cash", "AG1", "20514.62", "20514.62", "1", "3003"),
    ]);

    expect(result.postings[0].groupRef).toBe("3001");
    expect(result.postings[1].groupRef).toBe("3001");
  });

  it("pairs on the totals alone when no unit price is quoted", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "", "4001"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "4002"),
    ]);

    expect(result.postings[0].groupRef).toBe("4001");
    expect(result.postings[1].groupRef).toBe("4001");
  });

  it("leaves a cash row with no trade ungrouped rather than failing", () => {
    const result = convert([
      row("Cash Out For Buy", "Cash", "AG1", "-401", "401", "1", "608443430"),
    ]);

    expect(result.errors).toHaveLength(0);
    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].groupRef).toBeUndefined();
  });
});

// Money paid into a product account is reported as a run of three rows of the
// same amount -- the subscription credited, spent, and credited again as the
// money that lands -- with no security anywhere in it. Every row below is
// verbatim from the master exports. Left ungrouped the run is three
// single-posting groups, two of them transfers, so the account reports twice the
// contribution it received and a residual the size of the deposit lands in
// IMBALANCE.
describe("deposits into a product account", () => {
  const FULL =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,Exchange,Symbol,Type,Action";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([FULL, ...rows].join("\n"), { currency: "GBP" });

  // The 2025-04-15 lump sum into an ISA, and the cash management account row
  // that paid for it.
  const LUMP_SUM =
    "2025-04-15,2025-04-15,Cash In Lump Sum,Cash,Investment ISA,AS10000001,,20000,20000,1,971613428,Completed,,GBP,CASH,Cash";
  const SPEND =
    "2025-04-15,2025-04-15,Cash Out For Buy,Cash,Investment ISA,AS10000001,,-20000,20000,1,971613429,Completed,,GBP,CASH,Cash";
  const DEPARTURE =
    "2025-04-15,2025-04-15,Transfer Out From Cash Management Account,Cash,Cash Management Account,AW10000001,,-20000,20000,1,971613430,Completed,,GBP,CASH,Cash";
  const ARRIVAL =
    "2025-04-15,2025-04-15,Cash In,Cash,Investment ISA,AS10000001,,20000,20000,1,971613431,Completed,,GBP,CASH,Cash";

  it("groups the run so one deposit leaves one residual, not three", () => {
    const result = convert([LUMP_SUM, SPEND, DEPARTURE, ARRIVAL]);

    expect(result.errors).toEqual([]);
    expect(result.postings.map((tx) => tx.groupRef)).toEqual([
      "971613428",
      "971613428",
      undefined,
      "971613428",
    ]);
    // What each account is left with: the arrival and the departure it answers,
    // equal and opposite, which is the pair 0068 has to match. Ungrouped the ISA
    // offered two identical credits and an unexplained debit instead.
    expect(residuals(result.postings)).toEqual({
      "971613428": { GBP: "20000" },
      "#2": { GBP: "-20000" },
    });
  });

  it("keeps the run's declared transfers so its residual reads as one", () => {
    // The lump sum's declared TRANSFER is what routes the group's residual to
    // TRANSFER_CLEARING rather than IMBALANCE, and grouping does not rewrite
    // what the broker declared: the ambiguous Cash In keeps both its readings.
    const result = convert([LUMP_SUM, SPEND, ARRIVAL]);

    expect(result.postings.map((tx) => tx.brokerTxType)).toEqual([
      [TxType.TRANSFER],
      [TxType.TRADE_CASH],
      [TxType.TRADE_CASH, TxType.TRANSFER],
    ]);
  });

  it("leaves a lump sum with no run to post on its own", () => {
    // A deposit straight into the cash management account is money from outside
    // with nothing beside it to cancel. All three in the sample are this shape.
    const result = convert([
      "2024-05-30,2024-05-30,Cash In Lump Sum,Cash,Cash Management Account,AW10000001,,19999,19999,1,822942572,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0]!.groupRef).toBeUndefined();
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRANSFER]);
  });

  it("groups a run whose rows settled on different days", () => {
    // From a real export: the subscription and its spend completed on the 11th
    // and the arrival on the 14th, so the settlement date the trade passes group
    // on would split this run. The reference run does not.
    const result = convert([
      "2022-11-14,11 Nov 2022,Cash In Lump Sum,Cash,Investment Account,AG10000002,,19996,19996,1,559604931,Completed,,GBP,CASH,Cash",
      "2022-11-14,11 Nov 2022,Cash Out For Buy,Cash,Investment Account,AG10000002,,-19996,19996,1,559604932,Completed,,GBP,CASH,Cash",
      "2022-11-14,14 Nov 2022,Cash In,Cash,Investment Account,AG10000002,,19996,19996,1,559604933,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.postings.map((tx) => tx.groupRef)).toEqual([
      "559604931",
      "559604931",
      "559604931",
    ]);
  });

  it("separates two runs of nearly equal amount in one account", () => {
    // Both completed on the 14th, 19,996 and 19,995, references interleaved. Only
    // the amount agreeing to the penny keeps one run's rows out of the other's.
    const result = convert([
      "2022-11-14,14 Nov 2022,Cash In Lump Sum,Cash,Investment Account,AG10000002,,19995,19995,1,559677710,Completed,,GBP,CASH,Cash",
      "2022-11-14,14 Nov 2022,Cash In,Cash,Investment Account,AG10000002,,19996,19996,1,559604933,Completed,,GBP,CASH,Cash",
      "2022-11-14,14 Nov 2022,Cash Out For Buy,Cash,Investment Account,AG10000002,,-19995,19995,1,559677711,Completed,,GBP,CASH,Cash",
      "2022-11-14,14 Nov 2022,Cash In,Cash,Investment Account,AG10000002,,19995,19995,1,559677712,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.postings.map((tx) => tx.groupRef)).toEqual([
      "559677710",
      undefined,
      "559677710",
      "559677710",
    ]);
  });

  it("does not take a cash row the day's trade needs", () => {
    // The same day's ISA buy, with its own cash row 40.8k references away. A run
    // is built only from what the trade passes left, so neither claims the other's.
    const result = convert([
      LUMP_SUM,
      SPEND,
      ARRIVAL,
      "2025-04-15,2025-04-17,Cash Out For Buy,Cash,Investment ISA,AS10000001,,-19969.2,19969.2,1,971613493,Completed,,GBP,CASH,Cash",
      '2025-04-15,2025-04-17,Buy,"VANECK UCITS ETFS PLC, SEMICONDUCTOR UCITS ETF A GBP ACC (SMGB)",Investment ISA,AS10000001,,19976.7,769,25.97,971613494,Completed,LON,SMGB,ETF,Buy',
    ]);

    expect(result.postings.map((tx) => tx.groupRef)).toEqual([
      "971613428",
      "971613428",
      "971613428",
      "971613494",
      "971613494",
    ]);
  });
});

// Fidelity names a trade for the reason it happened, and names its cash leg to
// match. Every row below is taken from a real export, with the account numbers
// and references replaced; before these types were mapped the client rejected
// each one while the conversion script kept it, so the two disagreed about which
// rows survived.
describe("trades the broker names for their reason", () => {
  const FULL =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,Exchange,Symbol,Type,Action";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([FULL, ...rows].join("\n"), { currency: "GBP" });

  it("pairs a dividend reinvestment with the cash out that funded it", () => {
    const result = convert([
      '2022-02-11,15 Feb 2022,Buy From Dividend,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",21.75,17,1.19,442184379,Completed,LON,BGEU,ETF,Buy',
      '2022-02-11,15 Feb 2022,Cash Out For Dividend Reinvestment,Cash,Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",-20.15,20.15,1,442184374,Completed,,GBP,CASH,Cash',
    ]);

    expect(result.errors).toEqual([]);
    // A plain purchase: the dividend has already posted as its own Cash Dividend
    // row, so deriving a reinvestment income leg here would count it twice.
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.quantity).toBe("17");
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(result.postings[0]!.groupRef).toBe("442184379");
    expect(result.postings[1]!.groupRef).toBe("442184379");
  });

  it("pairs a rebate reinvestment with its cash out", () => {
    const result = convert([
      "2022-03-04,10 Mar 2022,Cash Out,Cash,SIPP - Pension Savings Account,AP10000002,M&G European Index Tracker,-7.81,7.81,1,452602191,Completed,,GBP,CASH,Cash",
      "2022-03-04,10 Mar 2022,Buy From Rebate,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,M&G European Index Tracker,7.81,9.24,0.85,452602204,Completed,LON,M&G European Index Tracker,FUND,Buy",
    ]);

    expect(result.errors).toEqual([]);
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[0]!.groupRef).toBe("452602204");
    expect(result.postings[1]!.groupRef).toBe("452602204");
    // The group does not weigh zero, and cannot: 9.24 units at the printed price
    // of 0.85 is 7.854 against a cash out of 7.81. The price is rounded to the
    // penny and the units are not, so the 0.044 is the source disagreeing with
    // itself. The imbalance report is where that belongs.
    expect(residuals(result.postings)).toEqual({ "452602204": { GBP: "0.044" } });
  });

  it("derives the income a reinvestment consumed, since no row reports it", () => {
    // The one trade in either export with no cash row anywhere beside it: the
    // income buys the units without arriving as money first.
    const result = convert([
      "2022-03-24,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,Baillie Gifford Responsible Global Equity Income B Inc,31.65,21.09,1.5,460143202,Completed,LON,Baillie Gifford Responsible Global Equity Income B Inc,FUND,Buy",
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
    expect(result.postings[1]!.groupRef).toBe(result.postings[0]!.groupRef);
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
      "2022-03-24,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,Baillie Gifford Responsible Global Equity Income B Inc,31.65,21.09,1.5,,Completed,LON,Baillie Gifford Responsible Global Equity Income B Inc,FUND,Buy",
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
      "2023-06-28,29 Jun 2023,Cash Out,Cash,SIPP - Pension Savings Account,AP10000002,,-12091.15,12091.15,1,661638572,Completed,,GBP,CASH,Cash",
      "2023-06-28,04 Jul 2023,Sell For Switch,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,,-12091.15,12147.03,1,661638570,Completed,LON,M&G European Index Tracker,FUND,Sell",
      "2023-06-28,29 Jun 2023,Buy For Switch,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638575,Completed,,GBP,CASH,Cash",
      "2023-06-28,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.errors).toEqual([]);
    // The buy side names cash as the asset, so it is a movement of money rather
    // than a purchase of a security called Cash, and it nets against its cash out.
    expect(result.postings[2]!.brokerTxType).toEqual([TxType.TRADE_CASH]);
    expect(result.postings[0]!.groupRef).toBe("661638575");
    expect(result.postings[2]!.groupRef).toBe("661638575");
    // The sale side is a real disposal, and its proceeds arrive as a Cash In.
    expect(result.postings[1]!.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(result.postings[1]!.groupRef).toBe("661638570");
    expect(result.postings[3]!.groupRef).toBe("661638570");
  });

  it("keeps a Cash In's declared set whether or not it paired with a sale", () => {
    // Cash In is money from outside the account or a sale's proceeds, and no
    // lookup on the broker's name can tell the two apart. The declared set says
    // exactly that, and pairing records itself in the group alone rather than
    // rewriting what the broker declared.
    const paired = convert([
      "2023-06-28,04 Jul 2023,Sell For Switch,M&G European Index Tracker,SIPP - Pension Savings Account,AP10000002,,-12091.15,12147.03,1,661638570,Completed,LON,M&G European Index Tracker,FUND,Sell",
      "2023-06-28,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,,GBP,CASH,Cash",
    ]);
    expect(paired.postings[1]!.brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
    expect(paired.postings[1]!.tradingCurrency).toBe("GBP");
    expect(paired.postings[1]!.groupRef).toBe("661638570");

    const alone = convert([
      "2023-06-28,04 Jul 2023,Cash In,Cash,SIPP - Pension Savings Account,AP10000002,,12091.15,12091.15,1,661638574,Completed,,GBP,CASH,Cash",
    ]);
    expect(alone.postings[0]!.brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
    expect(alone.postings[0]!.groupRef).toBeUndefined();
  });

  it("skips a cancelled transaction and its cancelled cash row", () => {
    const result = convert([
      '2024-07-22,22 Jul 2024,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ (EQQQ)",Investment Account,AG10000001,,0,0,373.51,847633602,Cancelled,LON,EQQQ,ETF,Sell',
      "2024-07-22,22 Jul 2024,Cash In From Sell,Cash,Investment Account,AG10000001,,0,0,1,847633604,Cancelled,,GBP,CASH,Cash",
      "2024-07-22,22 Jul 2024,Dealing Fee,Cash,Investment Account,AG10000001,,-7.5,0,0,100000000,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.errors).toEqual([]);
    // The fee and its expense leg; nothing from the trade that never happened.
    expect(result.postings).toHaveLength(2);
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.TRANSACTION_COST]);
  });
});

// The export says "Cash" in the same column it names a security in, so reading it
// through described money as an instrument. A posting is described by, and carries
// the identifier of, whatever resolves it. See docs/spec/archive-format.md.
describe("what a posting resolves to", () => {
  const FULL =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,Exchange,Symbol,Type,Action";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([FULL, ...rows].join("\n"), { currency: "GBP" });

  it("describes a cash posting by its currency and hints at it", () => {
    const result = convert([
      "2022-01-11,11 Jan 2022,Service Fee,Cash,Cash Management Account,AW10000001,,-3.24,3.24,1,428845305,Completed,,GBP,CASH,Cash",
    ]);

    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
    expect(result.postings[0]!.identifierHints.map((h) => [h.type, h.value])).toEqual([
      [IdentifierType.CURRENCY, "GBP"],
    ]);
    // Including the expense leg it balances against, which was already money.
    expect(result.postings[1]!.instrumentDescription).toBe("GBP");
  });

  it("describes a dividend by the currency it paid, not the payer", () => {
    // Source investment names the security that paid it. Describing the posting
    // by the payer would resolve the money into that holding; carrying the payer
    // elsewhere is 0049's to decide.
    const result = convert([
      '2022-02-11,11 Feb 2022,Cash Dividend,Cash,Investment ISA,AS10000002,"BAILLIE GIFFORD EUROPEAN GROWTH TST, ORD GBP0.025 (BGEU)",22.67,22.67,1,442183607,Completed,,GBP,CASH,Cash',
    ]);

    expect(result.postings[0]!.instrumentDescription).toBe("GBP");
    expect(result.postings[0]!.brokerTxType).toEqual([TxType.DIVIDEND]);
  });

  it("keeps a security's own description and offers its ticker", () => {
    const result = convert([
      '2022-02-08,10 Feb 2022,Sell,"WISE PLC, CLS A ORD GBP0.01 (WISE)",SIPP - Pension Savings Account,AP10000001,,-7266.49,1242,5.85,441416452,Completed,LON,WISE,STOCK,Sell',
    ]);

    expect(result.postings[0]!.instrumentDescription).toContain("WISE PLC");
    expect(result.postings[0]!.identifierHints.map((h) => [h.type, h.value, h.domain])).toEqual([
      [IdentifierType.MIC_TICKER, "WISE", "XLON"],
    ]);
  });

  it("offers no ticker for a fund, which has none to offer", () => {
    // The export repeats the fund's name in the symbol column, against a real
    // exchange. Passing that on as a ticker would resolve to nothing and leave the
    // name in the security master twice, so the row goes on its description alone.
    const result = convert([
      "2022-03-24,06 Apr 2022,Reinvestment From Income,Baillie Gifford Responsible Global Equity Income B Inc,Investment ISA,AS10000002,,31.65,21.09,1.5,460143202,Completed,LON,Baillie Gifford Responsible Global Equity Income B Inc,FUND,Buy",
    ]);

    expect(result.postings[0]!.identifierHints).toEqual([]);
    expect(result.postings[0]!.instrumentDescription).toContain("Baillie Gifford");
  });

  it("names the exchange a ticker belongs to, where it knows the MIC", () => {
    // A ticker identifies an instrument only within an exchange. An exchange with
    // no MIC here is passed on bare rather than under a guess.
    const result = convert([
      '2024-06-03,05 Jun 2024,Buy,"RHEINMETALL AG, ORD NPV (RHM)",Investment Account,AG10000001,,5000,10,500,1000000001,Completed,ETR,RHM,STOCK,Buy',
      '2024-06-03,05 Jun 2024,Buy,"SAFRAN SA, ORD EUR0.20 (SAF)",Investment Account,AG10000001,,5000,25,200,1000000002,Completed,XXX,SAF,STOCK,Buy',
    ]);

    expect(result.postings[0]!.identifierHints[0]!.domain).toBe("XETR");
    expect(result.postings[1]!.identifierHints[0]!.value).toBe("SAF");
    expect(result.postings[1]!.identifierHints[0]!.domain).toBe("");
  });
});

describe("counter-legs", () => {
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
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
    `8 Feb 2022,${completion},${type},"${investments}",Investment Account,${account},,${amount},${quantity},${price},${ref},Completed`;

  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  it("names the account a charge went to", () => {
    const result = convert([row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "441416483")]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[1].brokerTxType).toEqual([TxType.TRANSACTION_COST]);
    expect(result.postings[1].accountType).toBe(AccountType.EXPENSE);
    expect(result.postings[1].quantity).toBe("10");
    expect(result.postings[1].groupRef).toBe(result.postings[0].groupRef);
    expectGroupsBalance(result.postings);
  });

  it("names the account a dividend came from", () => {
    const result = convert([row("Cash Dividend", "Cash", "AG1", "23.40", "23.40", "1", "441416484")]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[1].accountType).toBe(AccountType.INCOME);
    expect(result.postings[1].quantity).toBe("-23.4");
    expectGroupsBalance(result.postings);
  });

  it("leaves a trade and its cash leg alone -- the source supplied both", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "441416454"),
    ]);

    expect(result.postings).toHaveLength(2);
    expectGroupsBalance(result.postings);
  });

  it("balances a trade, its cash leg and the charge reported beside it", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "441416452"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "441416454"),
      row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "441416483", "8 Feb 2022"),
    ]);

    expect(result.postings).toHaveLength(4);
    expectGroupsBalance(result.postings);
  });

  it("does not invent a leg for a transfer, whose other side is another account", () => {
    const result = convert([row("Cash In", "Cash", "AG1", "5000", "5000", "1", "441416485")]);

    expect(result.postings).toHaveLength(1);
    expect(result.postings[0].brokerTxType).toEqual([TxType.TRADE_CASH, TxType.TRANSFER]);
  });
});

describe("trade cash legs", () => {
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  // Typing these transfers made every trade group read as one, so its residual
  // was routed to TRANSFER_CLEARING instead of IMBALANCE.
  it("are TRADE_CASH, keeping their direction and currency", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Out For Buy,Cash,Investment Account,AG1,,-401,401,1,608443430,Completed",
      "8 Feb 2022,10 Feb 2022,Cash In From Sell,Cash,Investment Account,AG1,,7265.70,7265.70,1,441416454,Completed",
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
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  // The two sides of one transfer hop, verbatim from the master export. Their
  // references differ by 3, which is what tells this month's fee transfer from
  // last month's when the amounts are identical -- and what the ordinal is for.
  it("states what a reference number is comparable by, on every transcribed row", () => {
    const result = convert([
      "2022-05-06,2022-05-06,Transfer To Cash Management Account For Fees,Cash,SIPP,AP1,,-2.19,2.19,1,481052149,Completed",
      "2022-05-06,2022-05-06,Cash In Ring-fenced For Fees,Cash,Cash Management Account,AW1,,2.19,2.19,1,481052152,Completed",
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
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Interest,Cash,Investment Account,AG1,,1.18,1.18,1,REF/2022/A,Completed",
    ]);

    const c = result.postings[0].correlations[0]!;
    expect(c.token).toBe("REF/2022/A");
    expect(c.ordinal).toBeUndefined();
    expect(c.ordinalSpan).toBeUndefined();
    expect(c.match).toEqual([Match.EXACT]);
  });

  it("correlates nothing for a row the source gave no reference for", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Interest,Cash,Investment Account,AG1,,1.18,1.18,1,,Completed",
    ]);

    expect(result.postings[0].correlations).toEqual([]);
  });

  // A derived leg transcribes nothing, so it correlates with nothing. Copying
  // the correlation of the posting it mirrors would state that the source
  // correlated a row it never wrote.
  it("does not give a derived counter-leg a correlation", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Dividend,Cash,Investment Account,AG1,,23.40,23.40,1,441416483,Completed",
    ]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[0].correlations).toHaveLength(1);
    expect(result.postings[1].accountType).toBe(AccountType.INCOME);
    expect(result.postings[1].correlations).toEqual([]);
  });
});


describe("the source's own cash total", () => {
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  // The number the pairing rules actually match on. It is independent of
  // quantity * unit price, which the export rounds, so the two disagreeing is
  // evidence that two legs do not belong together.
  it("transcribes Amount on a security row, unsigned", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Sell,Legal & General Global Health,ISA,AG1,,20514.62,2676,7.67,795832439,Completed",
    ]);

    expect(result.postings[0].settlementAmount).toBe("20514.62");
    // Not quantity * unit price, which is 20524.92 here: the whole point is that
    // the two come from different fields.
    expect(result.postings[0].quantity).toBe("-2676");
  });

  it("keeps the magnitude of a purchase, which the export writes negative", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Buy,Legal & General Global Health,ISA,AG1,,-4487.98,585,7.67,795832440,Completed",
    ]);

    expect(result.postings[0].settlementAmount).toBe("4487.98");
  });

  // A cash row's quantity is already the amount, so stating it again would put
  // the same figure on the posting twice and leave two values to disagree.
  it("states nothing on a cash row", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash In From Sell,Cash,ISA,AG1,,20514.62,20514.62,1,795832441,Completed",
    ]);

    expect(result.postings[0].settlementAmount).toBeUndefined();
  });

  it("states nothing on a derived counter-leg", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Dividend,Cash,Investment Account,AG1,,23.40,23.40,1,441416483,Completed",
    ]);

    expect(result.postings).toHaveLength(2);
    expect(result.postings[1].accountType).toBe(AccountType.INCOME);
    expect(result.postings[1].settlementAmount).toBeUndefined();
  });
});
