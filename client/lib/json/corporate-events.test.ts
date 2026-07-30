import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { AssetClass, ExportCorporateEventRowSchema, ExportCoverageSchema, SplitRowSchema } from "@/gen/api/v1/api_pb";
import type { ExportCorporateEventRow } from "@/gen/api/v1/api_pb";
import { splitsToJson, parseSplitsJson } from "./corporate-events";

function makeSplitRow(firstKnownAt?: Date): ExportCorporateEventRow {
  return create(ExportCorporateEventRowSchema, {
    identifierType: "MIC_TICKER",
    identifierValue: "AAPL",
    identifierDomain: "XNAS",
    assetClass: AssetClass.STOCK,
    dataProvider: "massive",
    event: {
      case: "split",
      value: create(SplitRowSchema, {
        exDate: "2020-08-31",
        splitFrom: "1",
        splitTo: "4",
        firstKnownAt: firstKnownAt ? timestampFromDate(firstKnownAt) : undefined,
      }),
    },
  });
}

describe("splitsToJson", () => {
  it("serializes knowledge time as an ISO 8601 instant", () => {
    const knownAt = new Date("2015-03-04T09:30:00.000Z");
    const out = JSON.parse(splitsToJson([makeSplitRow(knownAt)]));
    expect(out.events[0].first_known_at).toBe("2015-03-04T09:30:00.000Z");
  });

  it("omits knowledge time when the row carries none", () => {
    const out = JSON.parse(splitsToJson([makeSplitRow()]));
    expect(out.events[0]).not.toHaveProperty("first_known_at");
  });
});

describe("parseSplitsJson", () => {
  it("round-trips knowledge time", () => {
    const knownAt = new Date("2015-03-04T09:30:00.000Z");
    const { splits, errors } = parseSplitsJson(splitsToJson([makeSplitRow(knownAt)]));
    expect(errors).toEqual([]);
    expect(splits).toHaveLength(1);
    expect(splits[0].firstKnownAt?.toISOString()).toBe(knownAt.toISOString());
  });

  it("accepts a file with no knowledge time, leaving the server to stamp it", () => {
    const { splits, errors } = parseSplitsJson(splitsToJson([makeSplitRow()]));
    expect(errors).toEqual([]);
    expect(splits[0].firstKnownAt).toBeUndefined();
  });

  it("reports an unparseable knowledge time rather than dropping it", () => {
    const json = JSON.stringify([
      {
        identifier_type: "MIC_TICKER",
        identifier_value: "AAPL",
        ex_date: "2020-08-31",
        split_from: "1",
        split_to: "4",
        first_known_at: "not-a-timestamp",
      },
    ]);
    const { splits, errors } = parseSplitsJson(json);
    expect(splits).toHaveLength(0);
    expect(errors).toHaveLength(1);
    expect(errors[0].field).toBe("first_known_at");
  });
});

describe("parseSplitsJson coverage", () => {
  const events = [
    { identifier_type: "MIC_TICKER", identifier_value: "AAPL", identifier_domain: "XNAS", ex_date: "2020-08-31", split_from: "1", split_to: "4" },
    { identifier_type: "MIC_TICKER", identifier_value: "TSLA", identifier_domain: "XNAS", ex_date: "2022-08-25", split_from: "1", split_to: "3" },
  ];

  it("accepts a bare array as events with no coverage", () => {
    const { splits, coverage, errors } = parseSplitsJson(JSON.stringify(events));
    expect(errors).toEqual([]);
    expect(splits).toHaveLength(2);
    expect(coverage).toEqual([]);
  });

  it("accepts the object form without coverage", () => {
    const { splits, coverage, errors } = parseSplitsJson(JSON.stringify({ events }));
    expect(errors).toEqual([]);
    expect(splits).toHaveLength(2);
    expect(coverage).toEqual([]);
  });

  it("expands a global declaration over every instrument in the file", () => {
    const json = JSON.stringify({
      events,
      coverage: [{ from: "2022-01-01", before: "2026-07-30" }],
    });
    const { coverage, errors } = parseSplitsJson(json);
    expect(errors).toEqual([]);
    expect(coverage.map((c) => c.identifierValue)).toEqual(["AAPL", "TSLA"]);
    expect(coverage[0].before).toBe("2026-07-30");
  });

  it("lets a specific declaration override the global", () => {
    const json = JSON.stringify({
      events,
      coverage: [
        { from: "2022-01-01", before: "2026-07-30" },
        { identifier_type: "MIC_TICKER", identifier_value: "TSLA", identifier_domain: "XNAS", from: "2022-06-01", before: "2023-01-01" },
      ],
    });
    const { coverage, errors } = parseSplitsJson(json);
    expect(errors).toEqual([]);
    expect(coverage).toEqual([
      expect.objectContaining({ identifierValue: "AAPL", from: "2022-01-01", before: "2026-07-30" }),
      expect.objectContaining({ identifierValue: "TSLA", from: "2022-06-01", before: "2023-01-01" }),
    ]);
  });

  it("reports a bad declaration without discarding the events", () => {
    const json = JSON.stringify({ events, coverage: [{ from: "2022-01-01", before: "2021-01-01" }] });
    const { splits, coverage, errors } = parseSplitsJson(json);
    expect(errors).toHaveLength(1);
    expect(errors[0].field).toBe("before");
    expect(splits).toHaveLength(2);
    expect(coverage).toEqual([]);
  });

  it("rejects an object with no events array", () => {
    const { errors } = parseSplitsJson(JSON.stringify({ coverage: [] }));
    expect(errors).toHaveLength(1);
    expect(errors[0].field).toBe("file");
  });
});

describe("corporate event JSON coverage round trip", () => {
  it("carries coverage spans through serialize and parse", () => {
    const coverage = [
      create(ExportCoverageSchema, {
        identifierType: "MIC_TICKER",
        identifierValue: "AAPL",
        identifierDomain: "XNAS",
        from: "2020-01-01",
        before: "2025-01-01",
      }),
    ];
    const json = splitsToJson([makeSplitRow()], coverage);
    const result = parseSplitsJson(json);
    expect(result.errors).toEqual([]);
    expect(result.coverage).toEqual([
      expect.objectContaining({
        identifierType: "MIC_TICKER",
        identifierValue: "AAPL",
        identifierDomain: "XNAS",
        from: "2020-01-01",
        before: "2025-01-01",
      }),
    ]);
  });

  it("omits the coverage key when the export supplied none", () => {
    expect(JSON.parse(splitsToJson([makeSplitRow()]))).not.toHaveProperty("coverage");
  });
});
