import { describe, expect, it } from "vitest";
import { droppedSummary, droppedTypes } from "./dropped";

const unknown = (type: string) => ({
  rowIndex: 1,
  field: "type",
  message: `Unknown transaction type: ${type}`,
});

describe("droppedTypes", () => {
  it("names each unrecognised broker type once, sorted", () => {
    expect(
      droppedTypes([unknown("Corporate Action"), unknown("Rights Issue"), unknown("Corporate Action")])
    ).toEqual(["Corporate Action", "Rights Issue"]);
  });

  it("ignores errors that are not about the type", () => {
    // A bad date is a dropped row too, but it names no type to add to the map.
    expect(droppedTypes([{ rowIndex: 2, field: "date", message: "Invalid or missing date" }])).toEqual([]);
  });

  it("returns nothing for a clean parse", () => {
    expect(droppedTypes([])).toEqual([]);
  });
});

describe("droppedSummary", () => {
  it("is null when nothing was dropped", () => {
    expect(droppedSummary([])).toBeNull();
  });

  it("counts rows and names types", () => {
    expect(droppedSummary([unknown("Rights Issue"), unknown("Rights Issue")])).toBe(
      "2 rows dropped: Rights Issue"
    );
  });

  it("still reports a count when no type can be named", () => {
    expect(droppedSummary([{ rowIndex: 2, field: "date", message: "Invalid or missing date" }])).toBe(
      "1 row dropped"
    );
  });
});
