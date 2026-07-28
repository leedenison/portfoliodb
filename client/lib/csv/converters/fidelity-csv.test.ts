import { describe, it, expect } from "vitest";
import { convertFidelityToStandard, FIDELITY_TYPE_TO_OFX } from "./fidelity-csv";

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
    expect(result.txs.length).toBe(1);
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
    expect(result.txs.length).toBe(types.length);
    expect(new Set(result.txs.map((tx) => tx.type))).toEqual(
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
        expect(result.txs[0]!.quantity).toBeCloseTo(tc.want, 10);
      });
    }

    it("nets a matched transfer pair to zero", () => {
      // These two rows are the same money leaving one wrapper and arriving in
      // another. Reading the unsigned Quantity made them sum to twice the value.
      const csv = [FULL_HEADER, cases[2]!.row, cases[3]!.row].join("\n");
      const result = convertFidelityToStandard(csv, { currency: "GBP" });
      const total = result.txs.reduce((sum, tx) => sum + tx.quantity, 0);
      expect(total).toBeCloseTo(0, 10);
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
