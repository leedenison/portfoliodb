import { describe, it, expect } from "vitest";
import { prefillFromPosition, validateRatio } from "./splits";

describe("validateRatio", () => {
  it("rejects empty inputs", () => {
    expect(validateRatio("", "2").ok).toBe(false);
    expect(validateRatio("1", "").ok).toBe(false);
    expect(validateRatio(undefined, "2").ok).toBe(false);
  });

  it("rejects non-numeric inputs", () => {
    expect(validateRatio("a", "2").ok).toBe(false);
    expect(validateRatio("1", "b").ok).toBe(false);
  });

  it("rejects zero or negative", () => {
    expect(validateRatio("0", "2").ok).toBe(false);
    expect(validateRatio("1", "0").ok).toBe(false);
    expect(validateRatio("-1", "2").ok).toBe(false);
  });

  it("accepts a normal 20-for-1 split", () => {
    const v = validateRatio("1", "20");
    expect(v.ok).toBe(true);
    expect(v.error).toBeUndefined();
    expect(v.warning).toBeUndefined();
  });

  it("warns on extreme ratios but still accepts", () => {
    const big = validateRatio("1", "200");
    expect(big.ok).toBe(true);
    expect(big.warning).toBeDefined();
    const tiny = validateRatio("1000", "1");
    expect(tiny.ok).toBe(true);
    expect(tiny.warning).toBeDefined();
  });
});

describe("prefillFromPosition", () => {
  it("computes 20-for-1 from a 50-share position with delta 950", () => {
    const r = prefillFromPosition(50, 950);
    expect(r.splitFrom).toBe("1");
    expect(r.splitTo).toBe("20");
  });

  it("returns nulls when pre is zero or negative", () => {
    expect(prefillFromPosition(0, 100)).toEqual({ splitFrom: null, splitTo: null });
    expect(prefillFromPosition(-10, 100)).toEqual({ splitFrom: null, splitTo: null });
  });

  it("returns nulls when post position is zero or negative (e.g. reverse split wipeout)", () => {
    expect(prefillFromPosition(10, -10)).toEqual({ splitFrom: null, splitTo: null });
    expect(prefillFromPosition(10, -20)).toEqual({ splitFrom: null, splitTo: null });
  });

  it("returns nulls when inputs are not finite", () => {
    expect(prefillFromPosition(NaN, 100)).toEqual({ splitFrom: null, splitTo: null });
    expect(prefillFromPosition(100, Infinity)).toEqual({ splitFrom: null, splitTo: null });
  });

  it("handles fractional ratios for reverse splits (1-for-2)", () => {
    const r = prefillFromPosition(100, -50);
    expect(r.splitFrom).toBe("1");
    expect(r.splitTo).toBe("0.5");
  });
});
