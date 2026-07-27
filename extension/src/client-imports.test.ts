/**
 * The extension reuses the client's React-free modules through the "@/*" alias
 * rather than reimplementing CSV parsing and the wire format. That alias spans
 * two npm projects and is declared twice -- in tsconfig.json for the type checker
 * and in vite.config.ts for the bundler -- so it is easy to half-break.
 *
 * Parsing a row here exercises the whole chain in one go: the alias resolves into
 * client/, the generated protobuf types under client/gen are present, and
 * papaparse resolves from the extension's own dependencies.
 */

import { describe, expect, it } from "vitest";
import { parseStandardCSV } from "@/lib/csv/standard";
import { TxType } from "@/gen/api/v1/api_pb";

describe("client module imports", () => {
  it("parses a standard CSV row into a Tx", () => {
    const csv = [
      "date,instrument_description,type,quantity,settlement_currency",
      "2026-01-15,AAPL - Apple Inc.,BUYSTOCK,10,GBP",
    ].join("\n");

    const result = parseStandardCSV(csv);

    expect(result.errors).toEqual([]);
    expect(result.txs).toHaveLength(1);
    expect(result.txs[0]!.type).toBe(TxType.BUYSTOCK);
    expect(result.txs[0]!.quantity).toBe(10);
  });
});
