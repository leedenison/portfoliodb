import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { assembleSystemArchive, partCounts } from "./assemble";
import { marshalSystem } from "./codec";
import { ExportSystemArchiveResponseSchema } from "@/gen/api/v1/api_pb";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import type { ExportSystemArchiveResponse } from "@/gen/api/v1/api_pb";

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
    expect(partCounts(doc)).toEqual([{ label: "inflation index values", count: 1 }]);
  });
});

describe("partCounts", () => {
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
    expect(partCounts(doc)).toEqual([{ label: "prices", count: 2 }]);
  });
});
