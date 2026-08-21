/**
 * OCC option symbols: validation, normalisation and construction.
 *
 * A mirror of server/derivative/parse.go, which is where the same rules run once
 * a symbol has reached the server. The two are kept together deliberately: a
 * converter that builds a symbol the server would reject has invented an
 * identifier, and the only way to know it would is to encode the same bounds.
 *
 * Layout: root(1-6) + expiry(6, YYMMDD) + right(1, C or P) + strike(8, encoded as
 * price x 1000). The padded form spaces the root out to six characters, giving
 * the 21-character symbol OCC itself writes; the compact form drops the padding
 * and is what the database stores.
 *
 * Every function returns undefined rather than throwing or guessing. A symbol
 * that will not parse names no contract, and offering one anyway is how a
 * contract with no OCC symbol acquires an identity that belongs to another.
 *
 * Kept free of React so the import extension can use it.
 */

import { Big } from "@/lib/decimal";

/** expiry(6) + right(1) + strike(8). Fixed, which is what locates the root. */
const SUFFIX_LEN = 15;

const SUFFIX_RE = /^\d{6}[CP]\d{8}$/;

const ROOT_RE = /^[A-Z0-9]{1,6}$/;

const ROOT_LEN = 6;

/**
 * The number of places a strike is encoded to: eight digits of thousandths. A
 * strike finer than this, or above 99999.999, names no contract the format can
 * express.
 */
const STRIKE_SCALE = 3;

const MAX_STRIKE = new Big(99_999_999);

const THOUSAND = new Big(10 ** STRIKE_SCALE);

/** A symbol taken apart: its compact form, and the root OCC issued it under. */
export interface OccParts {
  compact: string;
  root: string;
}

/**
 * A symbol taken apart, or undefined if the value is not one.
 *
 * This is the validator the rest of the module is built on. The length bound is
 * the root's: at least one character and at most six, on either side of a
 * suffix whose length is fixed.
 */
export function occParts(s: string): OccParts | undefined {
  const compact = s.trim().toUpperCase().replaceAll(" ", "");
  if (compact.length < SUFFIX_LEN + 1 || compact.length > SUFFIX_LEN + ROOT_LEN) {
    return undefined;
  }
  if (!SUFFIX_RE.test(compact.slice(-SUFFIX_LEN))) return undefined;
  return { compact, root: compact.slice(0, compact.length - SUFFIX_LEN) };
}

/** The compact form of a symbol, or undefined if it is not one. */
export function occCompact(s: string): string | undefined {
  return occParts(s)?.compact;
}

/** The 21-character space-padded form of a symbol, or undefined if it is not one. */
export function occPadded(s: string): string | undefined {
  const parts = occParts(s);
  if (parts === undefined) return undefined;
  return parts.root.padEnd(ROOT_LEN) + parts.compact.slice(-SUFFIX_LEN);
}

/**
 * The compact symbol these terms name, or undefined where they name none.
 *
 * The expiry is read in local time, matching how parseOfxDate builds a bare
 * YYYYMMDD, so a date constructed from the file's own digits encodes back to
 * them exactly.
 */
export function buildOcc(
  root: string,
  expiry: Date,
  putCall: "C" | "P",
  strike: Big,
): string | undefined {
  const sym = root.trim().toUpperCase();
  if (!ROOT_RE.test(sym)) return undefined;
  if (Number.isNaN(expiry.getTime())) return undefined;
  if (strike.lt(0)) return undefined;

  // Shifted rather than multiplied and rounded: a strike quoted to three places
  // or fewer -- which is every strike OCC can encode -- lands exactly, and one
  // quoted finer fails here rather than being rounded into a contract nobody
  // traded.
  const thousandths = strike.times(THOUSAND);
  if (!thousandths.eq(thousandths.round(0)) || thousandths.gt(MAX_STRIKE)) {
    return undefined;
  }

  const yy = pad2(expiry.getFullYear() % 100);
  const mm = pad2(expiry.getMonth() + 1);
  const dd = pad2(expiry.getDate());
  return sym + yy + mm + dd + putCall + thousandths.toFixed(0).padStart(8, "0");
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}
