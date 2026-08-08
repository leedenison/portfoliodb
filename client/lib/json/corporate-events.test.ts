import { describe, expect, it } from "vitest";
import { parseSplitsJson } from "./corporate-events";

// The exporter that produced these files is gone -- corporate events export as
// an admin archive part -- so the fixtures are written by hand, which is what a
// file this parser still has to read now looks like.
const SPLIT = {
  identifier_type: "MIC_TICKER",
  identifier_value: "AAPL",
  identifier_domain: "XNAS",
  asset_class: "STOCK",
  ex_date: "2020-08-31",
  split_from: "1",
  split_to: "4",
};

describe("parseSplitsJson", () => {
  it("round-trips knowledge time", () => {
    const knownAt = "2015-03-04T09:30:00.000Z";
    const { splits, errors } = parseSplitsJson(JSON.stringify({ events: [{ ...SPLIT, first_known_at: knownAt }] }));
    expect(errors).toEqual([]);
    expect(splits).toHaveLength(1);
    expect(splits[0].firstKnownAt?.toISOString()).toBe(knownAt);
  });

  it("accepts a file with no knowledge time, leaving the server to stamp it", () => {
    const { splits, errors } = parseSplitsJson(JSON.stringify({ events: [SPLIT] }));
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
