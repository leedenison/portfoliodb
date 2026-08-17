import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { assembleSystemArchive, assembleUserArchive, systemPartCounts, userPartCounts } from "./assemble";
import { marshalSystem, marshalUser } from "./codec";
import { ExportSystemArchiveResponseSchema, ExportUserArchiveResponseSchema } from "@/gen/api/v1/api_pb";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import { PluginCategory, TxType } from "@/gen/type/v1/type_pb";
import type { ExportSystemArchiveResponse, ExportUserArchiveResponse } from "@/gen/api/v1/api_pb";

const ENVELOPE = {
  formatVersion: 1,
  exportedAt: { seconds: 1753833600n, nanos: 0 },
  sourceInstance: "portfoliodb.example.com",
  kind: 1,
};

function stream(...items: Parameters<typeof create<typeof ExportSystemArchiveResponseSchema>>[1][]): ExportSystemArchiveResponse[] {
  return items.map((i) => create(ExportSystemArchiveResponseSchema, i));
}

describe("assembleSystemArchive", () => {
  it("refuses a stream that carried no envelope", () => {
    expect(() =>
      assembleSystemArchive(stream({ item: { case: "partBegin", value: { part: ArchivePart.PRICES } } })),
    ).toThrow(/no envelope/);
  });

  // The marker is the whole reason a part that is present and empty survives:
  // it creates the container, and an unselected part is never assigned at all.
  it("keeps a selected empty part present and an unselected part absent", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.PRICES } } },
      ),
    );
    expect(doc.prices).toBeDefined();
    expect(doc.prices?.groups).toHaveLength(0);
    expect(doc.instruments).toBeUndefined();
    // And it survives serialisation, which is what the downloaded file is.
    const json = marshalSystem(doc);
    expect(json).toContain('"prices":{}');
    expect(json).not.toContain('"instruments"');
  });

  it("files each item under the part it followed", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.INSTRUMENTS } } },
        {
          item: {
            case: "instrument",
            value: { currency: "USD", identifiers: [{ type: 11, value: "AAPL", domain: "XNAS", canonical: true }] },
          },
        },
        { item: { case: "partBegin", value: { part: ArchivePart.PRICES } } },
        {
          item: {
            case: "priceGroup",
            value: {
              instrument: { type: 11, value: "AAPL", domain: "XNAS" },
              rows: [{ priceDate: "2024-01-15", close: "185.90" }],
            },
          },
        },
      ),
    );
    expect(doc.instruments?.instruments).toHaveLength(1);
    expect(doc.prices?.groups).toHaveLength(1);
    expect(doc.prices?.groups[0].rows[0].close).toBe("185.90");
    expect(doc.corporateEvents).toBeUndefined();
  });

  // Inflation groups are keyed by currency rather than by an instrument, so
  // they are the one part whose group carries no identifier.
  it("files inflation groups by currency", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.INFLATION_INDICES } } },
        {
          item: {
            case: "inflationGroup",
            value: {
              currency: "GBP",
              rows: [{ month: "2024-01-01", indexValue: "131.5", baseYear: 2015 }],
            },
          },
        },
      ),
    );
    expect(doc.inflationIndices?.groups).toHaveLength(1);
    expect(doc.inflationIndices?.groups[0].currency).toBe("GBP");
    expect(doc.inflationIndices?.groups[0].rows[0].baseYear).toBe(2015);
    expect(systemPartCounts(doc)).toEqual([{ label: "inflation index values", count: 1 }]);
  });

  // Both fetch-block tables travel in one part, so a group holds blocks of more
  // than one category and the count is of blocks rather than of instruments.
  it("files fetch blocks from both fetchers into one group", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.FETCH_BLOCKS } } },
        {
          item: {
            case: "fetchBlockGroup",
            value: {
              instrument: { type: 15, value: "AAPL", domain: "XNAS" },
              blocks: [
                { category: PluginCategory.PRICE, pluginId: "eodhd", reason: "404" },
                { category: PluginCategory.CORPORATE_EVENT, pluginId: "massive", reason: "403" },
              ],
            },
          },
        },
      ),
    );
    expect(doc.fetchBlocks?.groups).toHaveLength(1);
    expect(doc.fetchBlocks?.groups[0].blocks).toHaveLength(2);
    expect(systemPartCounts(doc)).toEqual([{ label: "fetch blocks", count: 2 }]);
  });

  // Resolved and unresolved events travel together: the flag is the point of
  // the part, and the queue still waiting for one is the other half.
  it("files unhandled events with their resolved flag", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.UNHANDLED_EVENTS } } },
        {
          item: {
            case: "unhandledEventGroup",
            value: {
              instrument: { type: 15, value: "XYZ", domain: "XNAS" },
              events: [
                { eventType: "REVERSE_SPLIT", detail: "judged", resolved: true },
                { eventType: "MERGER", detail: "waiting" },
              ],
            },
          },
        },
      ),
    );
    expect(doc.unhandledEvents?.groups[0].events).toHaveLength(2);
    expect(doc.unhandledEvents?.groups[0].events[0].resolved).toBe(true);
    expect(doc.unhandledEvents?.groups[0].events[1].resolved).toBe(false);
    expect(systemPartCounts(doc)).toEqual([{ label: "unhandled corporate events", count: 2 }]);
  });

  // Plugin config is the one flat part: the stream carries a row per message
  // rather than a group, because a config row has no aggregate root above it.
  it("files plugin config rows without a group", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.PLUGIN_CONFIG } } },
        {
          item: {
            case: "pluginConfig",
            value: {
              pluginId: "eodhd",
              category: PluginCategory.PRICE,
              enabled: true,
              precedence: 20,
            },
          },
        },
      ),
    );
    expect(doc.pluginConfig?.configs).toHaveLength(1);
    expect(doc.pluginConfig?.configs[0].pluginId).toBe("eodhd");
    expect(systemPartCounts(doc)).toEqual([{ label: "plugin config rows", count: 1 }]);
  });
});

describe("systemPartCounts", () => {
  it("counts rows rather than groups, and says nothing about absent parts", () => {
    const doc = assembleSystemArchive(
      stream(
        { item: { case: "envelope", value: ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.PRICES } } },
        {
          item: {
            case: "priceGroup",
            value: {
              instrument: { type: 11, value: "AAPL", domain: "XNAS" },
              rows: [{ priceDate: "2024-01-15", close: "1" }, { priceDate: "2024-01-16", close: "2" }],
            },
          },
        },
      ),
    );
    expect(systemPartCounts(doc)).toEqual([{ label: "prices", count: 2 }]);
  });
});

describe("assembleUserArchive", () => {
  const USER_ENVELOPE = { ...ENVELOPE, kind: 2 };

  function userStream(
    ...items: Parameters<typeof create<typeof ExportUserArchiveResponseSchema>>[1][]
  ): ExportUserArchiveResponse[] {
    return items.map((i) => create(ExportUserArchiveResponseSchema, i));
  }

  it("refuses a stream that carried no envelope", () => {
    expect(() =>
      assembleUserArchive(userStream({ item: { case: "partBegin", value: { part: ArchivePart.PREFERENCES } } })),
    ).toThrow(/no envelope/);
  });

  // The marker creates the container for a whole-part message just as it does
  // for a series of items, so a part asked for and holding nothing survives.
  it("keeps a selected empty part present", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.PREFERENCES } } },
      ),
    );
    expect(doc.preferences).toBeDefined();
    expect(doc.preferences?.displayCurrency).toBeUndefined();
    expect(marshalUser(doc)).toContain('"preferences":{}');
  });

  it("files the whole-part message under its marker", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.PREFERENCES } } },
        {
          item: {
            case: "preferences",
            value: { displayCurrency: "GBP" },
          },
        },
      ),
    );
    expect(doc.preferences?.displayCurrency).toBe("GBP");
  });

  // Transactions arrive a window at a time, so the assembler accumulates them
  // rather than replacing the container as a whole-part message does.
  it("accumulates transaction windows across the stream", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.TXS } } },
        {
          item: {
            case: "txWindow",
            value: {
              broker: 1,
              source: "FIDELITY:archive:export",
              postings: [{ quantity: "10", instrumentDescription: "AAPL", brokerTxType: [TxType.TRADE_ASSET] }],
            },
          },
        },
        {
          item: {
            case: "txWindow",
            value: {
              broker: 2,
              source: "IBKR:archive:export",
              postings: [{ quantity: "5", instrumentDescription: "MSFT", brokerTxType: [TxType.TRADE_ASSET] }],
            },
          },
        },
      ),
    );
    expect(doc.txs?.windows).toHaveLength(2);
    expect(doc.txs?.windows[0].source).toBe("FIDELITY:archive:export");
    expect(doc.txs?.windows[1].source).toBe("IBKR:archive:export");
  });

  // A transaction part asked for over no transactions is present and empty, the
  // same statement the marker makes for every other part.
  it("keeps an empty transaction part present", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.TXS } } },
      ),
    );
    expect(doc.txs).toBeDefined();
    expect(doc.txs?.windows).toHaveLength(0);
    expect(marshalUser(doc)).toContain('"txs":{}');
  });

  // Declarations arrive a statement at a time -- one account read at one date --
  // so the assembler accumulates them as it does transaction windows.
  it("accumulates declaration statements across the stream", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.DECLARATIONS } } },
        {
          item: {
            case: "declarationStatement",
            value: {
              broker: 1,
              account: "Z1",
              asOfDate: "2024-01-31",
              declarations: [
                { instrument: { type: 11, value: "AAPL", domain: "XNAS" }, declaredQty: "100" },
                { instrument: { type: 11, value: "MSFT", domain: "XNAS" }, declaredQty: "50" },
              ],
            },
          },
        },
        {
          item: {
            case: "declarationStatement",
            value: {
              broker: 1,
              account: "Z1",
              asOfDate: "2024-02-29",
              declarations: [
                { instrument: { type: 11, value: "AAPL", domain: "XNAS" }, declaredQty: "120" },
              ],
            },
          },
        },
      ),
    );
    expect(doc.declarations?.statements).toHaveLength(2);
    expect(doc.declarations?.statements[0].asOfDate).toBe("2024-01-31");
    expect(doc.declarations?.statements[0].declarations).toHaveLength(2);
    expect(doc.declarations?.statements[1].asOfDate).toBe("2024-02-29");
  });

  // A declarations part asked for over no declarations is present and empty,
  // the same statement the marker makes for every other part.
  it("keeps an empty declarations part present", () => {
    const doc = assembleUserArchive(
      userStream(
        { item: { case: "envelope", value: USER_ENVELOPE } },
        { item: { case: "partBegin", value: { part: ArchivePart.DECLARATIONS } } },
      ),
    );
    expect(doc.declarations).toBeDefined();
    expect(doc.declarations?.statements).toHaveLength(0);
    expect(marshalUser(doc)).toContain('"declarations":{}');
  });
});

describe("userPartCounts", () => {
  // Settings rather than rows, so the preview and the job's own total agree.
  it("counts the settings the file states", () => {
    const doc = assembleUserArchive([
      create(ExportUserArchiveResponseSchema, { item: { case: "envelope", value: { ...ENVELOPE, kind: 2 } } }),
      create(ExportUserArchiveResponseSchema, { item: { case: "partBegin", value: { part: ArchivePart.PREFERENCES } } }),
      create(ExportUserArchiveResponseSchema, {
        item: { case: "preferences", value: { displayCurrency: "GBP" } },
      }),
    ]);
    expect(userPartCounts(doc)).toEqual([{ label: "preference settings", count: 1 }]);
  });

  // Postings rather than windows, so the preview reads the same number the
  // import job counts.
  it("counts postings for the transaction part", () => {
    const doc = assembleUserArchive([
      create(ExportUserArchiveResponseSchema, { item: { case: "envelope", value: { ...ENVELOPE, kind: 2 } } }),
      create(ExportUserArchiveResponseSchema, { item: { case: "partBegin", value: { part: ArchivePart.TXS } } }),
      create(ExportUserArchiveResponseSchema, {
        item: {
          case: "txWindow",
          value: {
            broker: 1,
            source: "FIDELITY:archive:export",
            postings: [
              { quantity: "10", instrumentDescription: "AAPL", brokerTxType: [TxType.TRADE_ASSET] },
              { quantity: "-1000", instrumentDescription: "USD", brokerTxType: [TxType.TRADE_ASSET] },
              { quantity: "5", instrumentDescription: "MSFT", brokerTxType: [TxType.TRADE_ASSET] },
            ],
          },
        },
      }),
    ]);
    expect(userPartCounts(doc)).toEqual([{ label: "postings", count: 3 }]);
  });

  // Declarations rather than statements, for the same reason.
  it("counts declarations for the declarations part", () => {
    const doc = assembleUserArchive([
      create(ExportUserArchiveResponseSchema, { item: { case: "envelope", value: { ...ENVELOPE, kind: 2 } } }),
      create(ExportUserArchiveResponseSchema, {
        item: { case: "partBegin", value: { part: ArchivePart.DECLARATIONS } },
      }),
      create(ExportUserArchiveResponseSchema, {
        item: {
          case: "declarationStatement",
          value: {
            broker: 1,
            account: "Z1",
            asOfDate: "2024-01-31",
            declarations: [
              { instrument: { type: 11, value: "AAPL", domain: "XNAS" }, declaredQty: "100" },
              { instrument: { type: 11, value: "MSFT", domain: "XNAS" }, declaredQty: "50" },
            ],
          },
        },
      }),
      create(ExportUserArchiveResponseSchema, {
        item: {
          case: "declarationStatement",
          value: {
            broker: 1,
            account: "Z2",
            asOfDate: "2024-01-31",
            declarations: [{ instrument: { type: 11, value: "AAPL", domain: "XNAS" }, declaredQty: "7" }],
          },
        },
      }),
    ]);
    expect(userPartCounts(doc)).toEqual([{ label: "holding declarations", count: 3 }]);
  });

  // A part present and empty is present with nothing in it, which is a
  // different statement from a part that was never asked for.
  it("reports an empty part as zero settings rather than omitting it", () => {
    const doc = assembleUserArchive([
      create(ExportUserArchiveResponseSchema, { item: { case: "envelope", value: { ...ENVELOPE, kind: 2 } } }),
      create(ExportUserArchiveResponseSchema, { item: { case: "partBegin", value: { part: ArchivePart.PREFERENCES } } }),
    ]);
    expect(userPartCounts(doc)).toEqual([{ label: "preference settings", count: 0 }]);
  });
});
