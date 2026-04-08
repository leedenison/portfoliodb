import { describe, it, expect } from "vitest";
import { extractSchwabSplits } from "./schwab";

describe("extractSchwabSplits", () => {
  it("returns empty result for empty file", () => {
    const result = extractSchwabSplits("");
    expect(result.splits).toEqual([]);
  });

  it("ignores rows whose action is not a stock split", () => {
    const csv = [
      "Date,Action,Symbol,Description,Quantity,Price,Amount,Account",
      "06/06/2022,Buy,AMZN,AMAZON.COM INC,5,150,-750,12345",
      "06/07/2022,Sell,AMZN,AMAZON.COM INC,2,160,320,12345",
    ].join("\n");
    const result = extractSchwabSplits(csv);
    expect(result.splits).toEqual([]);
  });

  it("extracts a Stock Split row with no embedded ratio", () => {
    const csv = [
      "Date,Action,Symbol,Description,Quantity,Amount,Account",
      "06/06/2022,Stock Split,AMZN,AMAZON.COM INC,950,,12345",
    ].join("\n");
    const result = extractSchwabSplits(csv);
    expect(result.errors).toEqual([]);
    expect(result.splits).toHaveLength(1);
    const s = result.splits[0]!;
    expect(s.exDate).toBe("2022-06-06");
    expect(s.identifier.value).toBe("AMZN");
    expect(s.identifier.type).toBe("MIC_TICKER");
    expect(s.splitFrom).toBeUndefined();
    expect(s.splitTo).toBeUndefined();
    expect(s.deltaShares).toBe("950");
    expect(s.account).toBe("12345");
  });

  it("dedupes splits across multiple accounts and sums delta shares", () => {
    const csv = [
      "Date,Action,Symbol,Description,Quantity,Account",
      "06/06/2022,Stock Split,AMZN,AMAZON.COM INC,950,11111",
      "06/06/2022,Stock Split,AMZN,AMAZON.COM INC,475,22222",
    ].join("\n");
    const result = extractSchwabSplits(csv);
    expect(result.splits).toHaveLength(1);
    expect(result.splits[0]!.deltaShares).toBe("1425");
    expect(result.splits[0]!.account).toBeUndefined();
  });

  it("emits an error when a split row has no parseable date", () => {
    const csv = [
      "Date,Action,Symbol,Quantity",
      "not-a-date,Stock Split,AMZN,950",
    ].join("\n");
    const result = extractSchwabSplits(csv);
    expect(result.splits).toEqual([]);
    expect(result.errors.some((e) => e.field === "date")).toBe(true);
  });
});
