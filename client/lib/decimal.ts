/**
 * Decimal parsing for the converters.
 *
 * Quantities, prices and money are decimal, and a JavaScript `number` is a
 * float64 that cannot hold them exactly. The converters author facts -- they
 * derive counter-legs and split netted totals -- so the postings they emit have
 * to sum back to the totals they came from. See
 * adr/0026-exact-decimals-bounded-by-closure.md.
 *
 * Nothing else on the client computes with these values: display renders them
 * and charts take `number`. Keeping the dependency here rather than in component
 * code matters because these modules are shared with the MV3 extension, which
 * carries the bundle cost.
 */

import Big from "big.js";

export { Big };

/**
 * Matches the wire format for a decimal field -- the same shape as the
 * protovalidate pattern the API's decimal strings carry.
 */
export const DECIMAL_RE = /^-?[0-9]+(\.[0-9]+)?$/;

/**
 * Parse a decimal, returning undefined rather than throwing on anything that is
 * not one.
 *
 * big.js throws on a malformed value and has no NaN, so every parser that used
 * to guard with isNaN goes through here instead. The format is checked before
 * constructing rather than catching, so a bad cell still produces the row-level
 * parse error it always did instead of throwing out of the whole file.
 *
 * The regex is deliberately narrower than what big.js accepts: it rejects
 * exponent notation and leading "+", neither of which the wire format allows.
 */
export function parseDecimal(s: string | undefined | null): Big | undefined {
  if (s == null) return undefined;
  const trimmed = s.trim();
  if (!DECIMAL_RE.test(trimmed)) return undefined;
  return new Big(trimmed);
}

/**
 * Parse a decimal that arrived as a JavaScript number -- a JSON body, or a
 * spreadsheet cell the runtime already coerced.
 *
 * The value has been through a float64 and whatever it lost is already lost;
 * this is the boundary where it stops losing more. Stringifying first is what
 * makes 0.1 parse as 0.1 rather than as its binary expansion.
 */
export function decimalFromNumber(n: number | undefined | null): Big | undefined {
  if (n == null || !Number.isFinite(n)) return undefined;
  return new Big(String(n));
}
