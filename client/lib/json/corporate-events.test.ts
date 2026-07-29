import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { AssetClass, ExportCorporateEventRowSchema, SplitRowSchema } from "@/gen/api/v1/api_pb";
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
    expect(out[0].first_known_at).toBe("2015-03-04T09:30:00.000Z");
  });

  it("omits knowledge time when the row carries none", () => {
    const out = JSON.parse(splitsToJson([makeSplitRow()]));
    expect(out[0]).not.toHaveProperty("first_known_at");
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
