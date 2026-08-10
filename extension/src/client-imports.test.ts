/**
 * The extension reuses the client's React-free modules through the "@/*" alias
 * rather than reimplementing CSV parsing and the wire format. That alias spans
 * two npm projects and is declared twice -- in tsconfig.json for the type checker
 * and in vite.config.ts for the bundler -- so it is easy to half-break.
 *
 * Splitting a row here exercises the whole chain in one go: the alias resolves
 * into client/, the generated protobuf types under client/gen are present, and
 * papaparse resolves from the extension's own dependencies.
 */

import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { parseCSVLine } from "@/lib/csv/line";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import { TxType } from "@/gen/type/v1/type_pb";

describe("client module imports", () => {
  it("splits a quoted CSV row through the client's parser", () => {
    expect(parseCSVLine('2026-01-15,"AAPL - Apple, Inc.",BUYSTOCK,10')).toEqual([
      "2026-01-15",
      "AAPL - Apple, Inc.",
      "BUYSTOCK",
      "10",
    ]);
  });

  it("builds an archive posting from the client's generated types", () => {
    const posting = create(PostingSchema, { brokerTxType: [TxType.TRADE_ASSET], quantity: "10" });

    expect(posting.brokerTxType).toEqual([TxType.TRADE_ASSET]);
    expect(posting.quantity).toBe("10");
  });
});
