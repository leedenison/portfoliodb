// Generates system archive files for tests that need more data than a fixture
// should carry.
//
// The document is written as protojson by hand rather than through the codec,
// which is worth doing on its own account: docs/spec/archive-format.md says a
// hand-written archive is a supported way to produce one, and nothing else
// checks that claim.

import { readFileSync, writeFileSync } from "fs";
import path from "path";
import { tmpdir } from "os";
import { fromJsonString } from "@bufbuild/protobuf";
import { SystemArchiveSchema, type SystemArchive } from "../gen/archive/v1/archive_pb";

export interface GeneratedArchive {
  /** Where the file was written. */
  path: string;
  /** How many instruments the instrument part carries. */
  instruments: number;
  /** How many price rows the price part carries in total. */
  priceRows: number;
  /** The identifier values used, in order. */
  tickers: string[];
}

/** Formats a day offset from 2024-01-01 as YYYY-MM-DD. */
function day(offset: number): string {
  const d = new Date(Date.UTC(2024, 0, 1));
  d.setUTCDate(d.getUTCDate() + offset);
  return d.toISOString().slice(0, 10);
}

/**
 * Write an archive carrying `instruments` instruments and `rowsEach` price rows
 * for each of them.
 *
 * The price groups deliberately declare no asset_class. A group that declares
 * one is routed through the identifier plugins, which in a test means a cassette
 * -- and a generated instrument has nothing to record against. Naming the
 * instruments in their own part instead gives them an asset class through
 * EnsureInstrument, which calls no plugin.
 */
export function writeGeneratedArchive(opts: {
  instruments: number;
  rowsEach: number;
  filename?: string;
  /**
   * Rows given a well-formed but impossible date, as [instrument, row] pairs.
   * The date still matches the field's pattern, so protovalidate passes it and
   * it becomes a row-level problem rather than a rejection of the whole file.
   */
  badDates?: [number, number][];
  /**
   * Rows given a close that is not a decimal at all. The field is
   * pattern-constrained, so this is refused for the whole document before any
   * of it is applied.
   */
  badDecimals?: [number, number][];
}): GeneratedArchive {
  const { instruments, rowsEach } = opts;
  const badDate = new Set((opts.badDates ?? []).map(([i, r]) => `${i}:${r}`));
  const badDecimal = new Set((opts.badDecimals ?? []).map(([i, r]) => `${i}:${r}`));
  const tickers: string[] = [];

  const instrumentPart: unknown[] = [];
  const priceGroups: unknown[] = [];

  for (let i = 0; i < instruments; i++) {
    const ticker = `E2E${String(i).padStart(4, "0")}`;
    tickers.push(ticker);
    // A security nests its currency lines, and the ticker names one of them, so
    // it goes on the line rather than beside the security.
    instrumentPart.push({
      asset_class: "STOCK",
      name: `E2E Test Instrument ${i}`,
      listings: [
        {
          currency: "USD",
          identifiers: [{ type: "MIC_TICKER", value: ticker, domain: "XNAS", canonical: true }],
        },
      ],
    });

    const rows: unknown[] = [];
    for (let r = 0; r < rowsEach; r++) {
      const key = `${i}:${r}`;
      rows.push({
        // 31 February matches the pattern and is not a date, which is exactly
        // the shape of problem that reaches the importer rather than being
        // caught at the edge.
        price_date: badDate.has(key) ? "2024-02-31" : day(r),
        close: badDecimal.has(key) ? "not-a-number" : (100 + (r % 50) + i / 1000).toFixed(4),
      });
    }
    // The group is one listing, named by an identifier and the currency saying
    // which of the security's lines it is.
    priceGroups.push({
      instrument: { type: "MIC_TICKER", value: ticker, domain: "XNAS", currency: "USD" },
      coverage: [{ from: day(0), before: day(rowsEach) }],
      rows,
    });
  }

  const doc = {
    envelope: {
      format_version: 2,
      exported_at: "2026-07-30T00:00:00Z",
      source_instance: "e2e-generated",
      kind: "SYSTEM",
    },
    instruments: { instruments: instrumentPart },
    prices: { groups: priceGroups },
  };

  const file = path.join(tmpdir(), opts.filename ?? "generated-archive.json");
  writeFileSync(file, JSON.stringify(doc));
  return { path: file, instruments, priceRows: instruments * rowsEach, tickers };
}

/**
 * Read an archive file the way a client does: protojson in, message out. A test
 * that generated the file still goes through the decode rather than around it.
 */
export function readArchive(file: string): SystemArchive {
  return fromJsonString(SystemArchiveSchema, readFileSync(file, "utf8"));
}
