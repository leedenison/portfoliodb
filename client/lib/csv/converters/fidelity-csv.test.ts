import { Big } from "@/lib/decimal";
import { describe, it, expect } from "vitest";
import { AccountType, TxType } from "@/gen/api/v1/api_pb";
import { convertFidelityToStandard, FIDELITY_TYPE_TO_OFX } from "./fidelity-csv";
import { expectGroupsBalance } from "@/lib/csv/group-balance.test-utils";

const HEADER =
  "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit";

describe("convertFidelityToStandard", () => {
  it("returns error when currency is missing", () => {
    const result = convertFidelityToStandard("Order date,Transaction type,Investments\n", {});
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors.some((e) => e.message.includes("Currency"))).toBe(true);
    expect(result.txs.length).toBe(0);
  });

  it("returns error when file is empty", () => {
    const result = convertFidelityToStandard("", { currency: "GBP" });
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.txs.length).toBe(0);
  });

  it("returns error when Fidelity header not found", () => {
    const result = convertFidelityToStandard("foo,bar\n1,2", { currency: "GBP" });
    expect(result.errors.length).toBeGreaterThan(0);
    expect(result.errors.some((e) => e.message.includes("Order date"))).toBe(true);
    expect(result.txs.length).toBe(0);
  });

  it("parses a single Sell row and uses Completion date", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status",
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10041188,,-31826.24,70,454.66,1229145354,Completed,',
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(1);
    expect(result.txs[0]!.instrumentDescription).toContain("INVESCO");
    expect(result.txs[0]!.quantity).toBe(-70);
    expect(result.txs[0]!.type).toBe(10); // SELLSTOCK
    expect(result.txs[0]!.settlementCurrency).toBe("GBP");
    expect(result.txs[0]!.tradingCurrency).toBe(""); // Sell is not Cash type
    expect(result.txs[0]!.account).toBe("AG10041188");
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
    expect(result.txs.length).toBe(1);
    expect(result.periodFrom.getFullYear()).toBe(2026);
    expect(result.periodFrom.getMonth()).toBe(0); // Jan
    expect(result.periodFrom.getDate()).toBe(21);
  });

  it("parses Cash Interest as INCOME", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "16 Feb 2026,23 Feb 2026,Cash Interest,Cash,AP10013127,3.27,1",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "USD" });
    expect(result.errors).toEqual([]);
    // The cash row plus the income it came from.
    expect(result.txs.length).toBe(2);
    expect(result.txs[0]!.type).toBe(11); // INCOME
    expect(result.txs[0]!.quantity).toBe(3.27);
    expect(result.txs[0]!.settlementCurrency).toBe("USD");
  });

  // The exported map is the set of types the converter accepts. Consumers that
  // upload despite dropped rows use it to name the types they dropped, so a type
  // present in the map must not be rejected by the converter.
  it("accepts every transaction type in FIDELITY_TYPE_TO_OFX", () => {
    const types = Object.keys(FIDELITY_TYPE_TO_OFX);
    const csv = [
      HEADER,
      ...types.map((t) => `20 Oct 2025,22 Oct 2025,${t},ISHARES II PLC INRG,SIPP,1,7.16`),
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    // Counter-legs are appended for the one-sided rows, so count the postings
    // that came from a source row: those are the ones a type maps to.
    const source = result.txs.filter((tx) => tx.accountType === AccountType.UNSPECIFIED);
    expect(source.length).toBe(types.length);
    expect(new Set(source.map((tx) => tx.type))).toEqual(
      new Set(Object.values(FIDELITY_TYPE_TO_OFX))
    );
  });

  it("names the offending type when a row's type is unrecognised", () => {
    const csv = [
      HEADER,
      "20 Oct 2025,22 Oct 2025,Corporate Action Reinvestment,ISHARES II PLC INRG,SIPP,1,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.txs).toEqual([]);
    expect(result.errors).toHaveLength(1);
    expect(result.errors[0]!.field).toBe("type");
    expect(result.errors[0]!.message).toContain("Corporate Action Reinvestment");
  });

  // Rows below are taken verbatim from a real export. The full header is needed
  // because the Amount column carries the direction of a cash movement.
  const FULL_HEADER =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status,";

  describe("cash movement direction", () => {
    const cases: { name: string; row: string; want: number }[] = [
      {
        name: "a fee is an outflow",
        row: '03 Jul 2026,08 Jul 2026,Service Fee,"Cash",Cash Management Account,AW10075724,,-5.20,5.2,1,1314200860,Completed,',
        want: -5.2,
      },
      {
        name: "interest is an inflow",
        row: '15 Jul 2026,21 Jul 2026,Cash Interest,"Cash",Investment ISA,AS10110796,"Cash",1.26,1.26,1,1319149500,Completed,',
        want: 1.26,
      },
      {
        name: "a transfer out is an outflow",
        row: '03 Jul 2026,03 Jul 2026,Transfer To Cash Management Account For Fees,"Cash",Investment ISA,AS10110796,,-2.30,2.3,1,1313264690,Completed,',
        want: -2.3,
      },
      {
        name: "the matching transfer in is an inflow",
        row: '03 Jul 2026,03 Jul 2026,Cash In Ring-fenced For Fees,"Cash",Cash Management Account,AW10075724,,2.30,2.3,1,1313264694,Completed,',
        want: 2.3,
      },
      {
        name: "Quantity of zero does not lose the amount",
        row: '15 Jul 2026,21 Jul 2026,Tax On Interest,"Cash",Investment Account,AG10041188,,-0.20,0,0,267898807,Completed,',
        want: -0.2,
      },
    ];

    for (const tc of cases) {
      it(tc.name, () => {
        const result = convertFidelityToStandard([FULL_HEADER, tc.row].join("\n"), { currency: "GBP" });
        expect(result.errors).toEqual([]);
        expect(result.txs[0]!.quantity).toBe(tc.want);
      });
    }

    it("nets a matched transfer pair to zero", () => {
      // These two rows are the same money leaving one wrapper and arriving in
      // another. Reading the unsigned Quantity made them sum to twice the value.
      const csv = [FULL_HEADER, cases[2]!.row, cases[3]!.row].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      // Summed in decimal so the assertion holds for any pair rather than for
      // ones whose float64 sum happens to land on zero.
      const total = result.txs.reduce((sum, tx) => sum.plus(tx.quantity), new Big(0));
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
        `10 Apr 2025,14 Apr 2025,${type},"Cash",Investment ISA,AS10110796,,-100.00,100,1,123,Completed,`,
      ].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      expect(result.errors).toEqual([]);
      expect(result.txs).toHaveLength(1);
      expect(result.txs[0]!.quantity).toBe(-100);
    }
  );

  it("keeps share counts for security rows, negating sells", () => {
    const csv = [
      FULL_HEADER,
      '21 Jan 2026,23 Jan 2026,Sell,"INVESCO MARKETS III PLC, INVESCO EQQQ NASDAQ 100 UCITS ETF (EQQQ)",Investment Account,AG10041188,,-31826.24,70,454.66,1229145354,Completed,',
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    // Quantity is a share count here, not money, so Amount must not replace it.
    expect(result.txs[0]!.quantity).toBe(-70);
  });

  it("falls back to Quantity when the export has no Amount column", () => {
    const csv = [
      HEADER,
      "15 Jul 2026,21 Jul 2026,Cash Interest,Cash,AS10110796,1.26,1",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.txs[0]!.quantity).toBe(1.26);
  });

  it("parses Buy with positive quantity", () => {
    const csv = [
      "Order date,Completion date,Transaction type,Investments,Account Number,Quantity,Price per unit",
      "20 Oct 2025,22 Oct 2025,Buy,ISHARES II PLC INRG,SIPP,12783,7.16",
    ].join("\n");
    const result = convertFidelityToStandard(csv, { currency: "GBP" });
    expect(result.errors).toEqual([]);
    expect(result.txs.length).toBe(1);
    expect(result.txs[0]!.type).toBe(5); // BUYSTOCK
    expect(result.txs[0]!.quantity).toBe(12783);
    expect(result.txs[0]!.settlementCurrency).toBe("GBP");
    expect(result.txs[0]!.tradingCurrency).toBe(""); // Buy is not Cash type
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
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "5.85", "563466569"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "563466571"),
    ]);

    expect(result.txs).toHaveLength(2);
    expect(result.txs[0].groupRef).toBe("563466569");
    expect(result.txs[1].groupRef).toBe("563466569");
  });

  it("pairs a buy with the cash out despite the fee gap", () => {
    // The buy row's Amount includes a 10.00 dealing fee that Fidelity posts as its
    // own row, so the cash out is 10.00 smaller.
    const result = convert([
      row("Cash Out For Buy", "Cash", "AG1", "-7380.19", "7380.19", "1", "563466631"),
      row("Buy", "INVESCO EQQQ (EQQQ)", "AG1", "7390.19", "28", "263.58", "563466632"),
    ]);

    expect(result.txs[0].groupRef).toBe("563466632");
    expect(result.txs[1].groupRef).toBe("563466632");
  });

  it("keeps a separately reported charge out of the trade's group", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "5.85", "563466569"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "563466571"),
      row("Dealing Fee", "Cash", "AG1", "-10", "0", "0", "563466600", "8 Feb 2022"),
    ]);

    // It has a group of its own -- it needs one to hold its expense leg -- but
    // Fidelity dates it on the order date while the trade settles later, so
    // folding it into the trade would misdate it.
    expect(result.txs[2].groupRef).not.toBe("");
    expect(result.txs[2].groupRef).not.toBe(result.txs[0].groupRef);
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
    expect(result.txs[0].groupRef).toBe("1001");
    expect(result.txs[3].groupRef).toBe("1001");
    expect(result.txs[1].groupRef).toBe("1003");
    expect(result.txs[2].groupRef).toBe("1003");
  });

  it("does not let one buy strand another by claiming its cash row", () => {
    // The 29939.31 buy sits one reference from the 36946.72 cash out, but a fee
    // cannot be negative, so that cash row can only belong to the larger buy.
    const result = convert([
      row("Cash Out For Buy", "Cash", "AP1", "-29931.81", "29931.81", "1", "819092458"),
      row("Buy", "VANGUARD S&P 500 (VUSA)", "AP1", "29939.31", "100", "299.39", "819092460"),
      row("Cash Out For Buy", "Cash", "AP1", "-36946.72", "36946.72", "1", "819092461"),
      row("Buy", "INVESCO EQQQ (EQQQ)", "AP1", "36954.22", "120", "307.95", "819092463"),
    ]);

    expect(result.txs[1].groupRef).toBe("819092460");
    expect(result.txs[0].groupRef).toBe("819092460");
    expect(result.txs[3].groupRef).toBe("819092463");
    expect(result.txs[2].groupRef).toBe("819092463");
  });

  it("does not pair a sell across accounts or settlement dates", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-100.00", "10", "10", "1001"),
      row("Cash In From Sell", "Cash", "AS2", "100.00", "100", "1", "1002"),
      row("Cash In From Sell", "Cash", "AG1", "100.00", "100", "1", "1003", "11 Feb 2022"),
    ]);

    for (const tx of result.txs) {
      expect(tx.groupRef).toBe("");
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

    for (const tx of result.txs) {
      expect(tx.groupRef).toBe("");
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

    expect(result.txs[1].groupRef).toBe("2002");
    expect(result.txs[2].groupRef).toBe("2002");
    expect(result.txs[3].groupRef).toBe("2004");
    expect(result.txs[0].groupRef).toBe("2004");
  });

  it("tolerates the rounding in a quoted unit price", () => {
    // 2676 * 7.67 is 20,524.92 while the broker settled 20,514.62 -- 0.05% out,
    // because the export rounds the price. A tighter check would reject real trades.
    const result = convert([
      row("Sell", "ISHARES INRG (INRG)", "AG1", "-20514.62", "2676", "7.67", "3001"),
      row("Cash In From Sell", "Cash", "AG1", "20514.62", "20514.62", "1", "3003"),
    ]);

    expect(result.txs[0].groupRef).toBe("3001");
    expect(result.txs[1].groupRef).toBe("3001");
  });

  it("pairs on the totals alone when no unit price is quoted", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7266.49", "1242", "", "4001"),
      row("Cash In From Sell", "Cash", "AG1", "7266.49", "7266.49", "1", "4002"),
    ]);

    expect(result.txs[0].groupRef).toBe("4001");
    expect(result.txs[1].groupRef).toBe("4001");
  });

  it("leaves a cash row with no trade ungrouped rather than failing", () => {
    const result = convert([
      row("Cash Out For Buy", "Cash", "AG1", "-401", "401", "1", "730493547"),
    ]);

    expect(result.errors).toHaveLength(0);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].groupRef).toBe("");
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
    const result = convert([row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "563466600")]);

    expect(result.txs).toHaveLength(2);
    expect(result.txs[1].type).toBe(TxType.INVEXPENSE);
    expect(result.txs[1].accountType).toBe(AccountType.EXPENSE);
    expect(result.txs[1].quantity).toBe(10);
    expect(result.txs[1].groupRef).toBe(result.txs[0].groupRef);
    expectGroupsBalance(result.txs);
  });

  it("names the account a dividend came from", () => {
    const result = convert([row("Cash Dividend", "Cash", "AG1", "23.40", "23.40", "1", "563466601")]);

    expect(result.txs).toHaveLength(2);
    expect(result.txs[1].accountType).toBe(AccountType.INCOME);
    expect(result.txs[1].quantity).toBe(-23.4);
    expectGroupsBalance(result.txs);
  });

  it("leaves a trade and its cash leg alone -- the source supplied both", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "563466569"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "563466571"),
    ]);

    expect(result.txs).toHaveLength(2);
    expectGroupsBalance(result.txs);
  });

  it("balances a trade, its cash leg and the charge reported beside it", () => {
    const result = convert([
      row("Sell", "WISE PLC (WISE)", "AG1", "-7265.70", "1242", "5.85", "563466569"),
      row("Cash In From Sell", "Cash", "AG1", "7265.70", "7265.70", "1", "563466571"),
      row("Dealing Fee", "Cash", "AG1", "-10", "10", "1", "563466600", "8 Feb 2022"),
    ]);

    expect(result.txs).toHaveLength(4);
    expectGroupsBalance(result.txs);
  });

  it("does not invent a leg for a journal, whose other side is another account", () => {
    const result = convert([row("Cash In", "Cash", "AG1", "5000", "5000", "1", "563466602")]);

    expect(result.txs).toHaveLength(1);
    expect(result.txs[0].type).toBe(TxType.JRNLFUND);
  });
});

describe("trade cash legs", () => {
  const HEAD =
    "Order date,Completion date,Transaction type,Investments,Product Wrapper,Account Number,Source investment,Amount,Quantity,Price per unit,Reference Number,Status";
  const convert = (rows: string[]) =>
    convertFidelityToStandard([HEAD, ...rows].join("\n"), { currency: "GBP" });

  // Typing these JRNLFUND made every trade group read as a transfer, so its
  // residual was routed to TRANSFER_CLEARING instead of IMBALANCE.
  it("are CASHFLOW, keeping their direction and currency", () => {
    const result = convert([
      "8 Feb 2022,10 Feb 2022,Cash Out For Buy,Cash,Investment Account,AG1,,-401,401,1,730493547,Completed",
      "8 Feb 2022,10 Feb 2022,Cash In From Sell,Cash,Investment Account,AG1,,7265.70,7265.70,1,563466571,Completed",
    ]);

    expect(result.txs[0].type).toBe(TxType.CASHFLOW);
    expect(result.txs[0].quantity).toBe(-401);
    expect(result.txs[0].tradingCurrency).toBe("GBP");
    expect(result.txs[1].type).toBe(TxType.CASHFLOW);
    expect(result.txs[1].quantity).toBe(7265.7);
  });
});
