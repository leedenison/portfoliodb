import { describe, it, expect } from "vitest";
import { convertFidelityToStandard } from "./fidelity";

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

  it("parses a single Sell row and uses Order date", () => {
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
    expect(result.txs[0]!.currency).toBe("GBP");
    expect(result.txs[0]!.account).toBe("AG10041188");
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
    expect(result.txs[0]!.currency).toBe("USD");
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
  });
});
