/**
 * What a converter returns: the contents of one archive transaction window.
 *
 * A converter reads a broker's own file and produces archive postings, which is
 * the one format PortfolioDB defines for transactions in either direction. The
 * broker and the source are the caller's -- they come from which converter was
 * chosen, not from the file -- so what the converter states is the postings and
 * the period they cover. See docs/spec/archive-format.md.
 *
 * Kept free of React so the import extension can use it.
 */

import type { Posting } from "@/gen/archive/v1/txs_pb";

export interface ParseError {
  rowIndex: number;
  field: string;
  message: string;
}

export interface StandardParseResult {
  postings: Posting[];
  periodFrom: Date;
  /** Exclusive: local midnight after the last transaction's day. */
  periodBefore: Date;
  /**
   * When the file says it was written, absent when the format does not say.
   *
   * It is the vintage of every identifier in the file: a broker names an option
   * under the symbol current at its export, not under the one the contract wore
   * on each trade date. It is what dates the names a resolution writes from
   * these hints, so a symbol a split has since restated is stored as correct
   * from here rather than from today. A converter reports what the file states
   * and never a substitute -- the upload decides what to do about a format that
   * states nothing, and a guess made here would be indistinguishable from the
   * file's own claim. See docs/spec/bitemporality.md.
   */
  exportedAt?: Date;
  errors: ParseError[];
}
