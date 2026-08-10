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
  errors: ParseError[];
}
