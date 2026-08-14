import { describe, expect, it } from "vitest";
import { formatCurrency, formatCurrencyCompact } from "./format";

/**
 * Rendering money. Both take an explicit locale from nowhere -- Intl uses the
 * runtime's -- so these assert the shape rather than the exact separators, which
 * are the runtime's to choose and would make the tests about the test machine.
 */

describe("formatCurrency", () => {
  it("renders a numeric string without going through a float", () => {
    // The reason a string is accepted at all: an exact balance carries more
    // significant digits than a float64 holds, and Intl formats the string
    // directly. The value is 2^53 + 1 with cents on it, so a number would have
    // rounded it before it got here -- the last assertion says so. The group
    // separators are the runtime's, hence the strip.
    const exact = "9007199254740993.45";
    const digits = formatCurrency(exact, "USD").replace(/[^\d.]/g, "");
    expect(digits).toBe("9007199254740993.45");
    expect(Number(exact).toString()).not.toBe(exact);
  });

  it("always shows two decimal places", () => {
    // Money is written to the cent whether or not the value has one, so a column
    // of figures lines up.
    for (const v of [1, 1.5, 1.005, 0]) {
      expect(formatCurrency(v, "USD")).toMatch(/\d[.,]\d\d(\D|$)/);
    }
  });

  it("renders the currency it was given rather than the runtime's", () => {
    expect(formatCurrency(1, "GBP")).toMatch(/£|GBP/);
    expect(formatCurrency(1, "JPY")).toMatch(/¥|JPY/);
  });

  it("defaults to USD", () => {
    expect(formatCurrency(1)).toBe(formatCurrency(1, "USD"));
  });

  it("keeps the sign on a negative", () => {
    expect(formatCurrency(-1234.56, "USD")).toMatch(/-|\(/);
  });

  it("renders zero rather than nothing", () => {
    expect(formatCurrency(0, "USD")).toMatch(/0/);
  });
});

describe("formatCurrencyCompact", () => {
  it.each([
    [1_200_000, /1\.2\s*M/],
    [45_300, /45\.3\s*K/],
    [1_500_000_000, /1\.5\s*B/],
  ])("abbreviates %d", (value, want) => {
    expect(formatCurrencyCompact(value, "USD")).toMatch(want);
  });

  it("leaves a small value unabbreviated", () => {
    expect(formatCurrencyCompact(42, "USD")).toMatch(/42/);
  });

  it("keeps at most one decimal place", () => {
    // The point of compact notation is a figure that fits in a tile.
    expect(formatCurrencyCompact(1_234_567, "USD")).not.toMatch(/\d[.,]\d\d/);
  });

  it("keeps the sign on a negative", () => {
    expect(formatCurrencyCompact(-1_200_000, "USD")).toMatch(/-|\(/);
  });

  it("defaults to USD", () => {
    expect(formatCurrencyCompact(1_200_000)).toBe(formatCurrencyCompact(1_200_000, "USD"));
  });
});
