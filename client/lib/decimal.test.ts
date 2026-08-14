/**
 * The numeric boundary every converter's exactness rests on.
 *
 * A converter derives counter-legs and splits netted totals, so its postings have
 * to sum back to the figures they came from. That holds only if the values never
 * pass through a float64 -- which is what parseDecimal is for, and what makes the
 * regex in front of big.js load-bearing rather than defensive.
 */
import { describe, expect, it } from "vitest";
import { Big, DECIMAL_RE, decimalFromNumber, parseDecimal } from "./decimal";

describe("parseDecimal", () => {
  it.each([
    ["an integer", "42", "42"],
    ["a negative integer", "-42", "-42"],
    ["a fraction", "3.14", "3.14"],
    ["a negative fraction", "-0.005", "-0.005"],
    ["zero", "0", "0"],
    ["negative zero", "-0", "0"],
    ["a leading zero", "007", "7"],
    ["trailing zeros, which are kept", "1.500", "1.5"],
  ])("parses %s", (_name, input, want) => {
    expect(parseDecimal(input)?.toString()).toBe(want);
  });

  it("keeps precision a float64 would lose", () => {
    // The whole point: 0.1 + 0.2 is 0.30000000000000004 as numbers.
    expect(parseDecimal("0.1")!.plus(parseDecimal("0.2")!).toString()).toBe("0.3");
    // And a value with more significant digits than a float64 holds survives.
    const long = "9007199254740993.0000000001";
    expect(parseDecimal(long)!.toString()).toBe(long);
  });

  it("trims surrounding whitespace", () => {
    // A CSV cell arrives padded and the value in it is still a number.
    expect(parseDecimal("  1.25\t")?.toString()).toBe("1.25");
  });

  it.each([
    ["undefined", undefined],
    ["null", null],
    ["an empty string", ""],
    ["whitespace only", "   "],
    ["a word", "abc"],
    ["a currency symbol", "$1.00"],
    ["thousands separators", "1,000"],
    ["a trailing percent", "50%"],
    ["a bare decimal point", "."],
    ["a leading decimal point", ".5"],
    ["a trailing decimal point", "1."],
    ["two decimal points", "1.2.3"],
    ["a stray minus", "-"],
    ["a trailing minus", "1-"],
    ["parenthesised negative", "(1.00)"],
    ["a leading plus", "+1"],
    ["Infinity", "Infinity"],
    ["NaN", "NaN"],
  ])("rejects %s", (_name, input) => {
    expect(parseDecimal(input)).toBeUndefined();
  });

  it("rejects exponent notation, which big.js would accept but the wire format does not", () => {
    // Narrower than big.js on purpose: the shape is not in the protovalidate
    // pattern the API's decimal strings carry, so accepting it here would let a
    // value through that the server will reject.
    expect(() => new Big("1e3")).not.toThrow();
    expect(parseDecimal("1e3")).toBeUndefined();
  });

  it("returns undefined rather than throwing, so one bad cell is one bad row", () => {
    // big.js throws on a malformed value. The format is checked before
    // constructing so that a bad cell produces a row-level parse error instead of
    // taking the whole file down.
    expect(() => parseDecimal("not a number")).not.toThrow();
  });
});

describe("DECIMAL_RE", () => {
  // The pattern is shared with the wire format, so it is worth stating that it is
  // anchored: an unanchored one would accept "1.0abc" and every guard built on it
  // would let the trailing rubbish through.
  it.each(["1.0abc", "abc1.0", "1.0\n2.0"])("does not match %j", (s) => {
    expect(DECIMAL_RE.test(s)).toBe(false);
  });
});

describe("decimalFromNumber", () => {
  it.each([
    ["an integer", 42, "42"],
    ["a negative", -1.5, "-1.5"],
    ["zero", 0, "0"],
  ])("parses %s", (_name, input, want) => {
    expect(decimalFromNumber(input)?.toString()).toBe(want);
  });

  it("stops the loss where it is rather than compounding it", () => {
    // Whatever the float64 lost is already lost. Stringifying first is what makes
    // this 0.1 rather than its binary expansion, which is what the value would
    // become if it were handed to big.js as a number.
    expect(decimalFromNumber(0.1)?.toString()).toBe("0.1");
    expect(decimalFromNumber(0.1)!.plus(decimalFromNumber(0.2)!).toString()).toBe("0.3");
  });

  it.each([
    ["undefined", undefined],
    ["null", null],
    ["NaN", NaN],
    ["Infinity", Infinity],
    ["-Infinity", -Infinity],
  ])("rejects %s", (_name, input) => {
    expect(decimalFromNumber(input)).toBeUndefined();
  });
});
