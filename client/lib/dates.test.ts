import { describe, it, expect } from "vitest";
import { dayAfter, dayBefore, fromDayInput, toDayInput } from "./dates";

describe("dayAfter", () => {
  it("advances one day", () => {
    expect(dayAfter("2024-03-14")).toBe("2024-03-15");
  });

  it("rolls over month end", () => {
    expect(dayAfter("2024-04-30")).toBe("2024-05-01");
  });

  it("rolls over year end", () => {
    expect(dayAfter("2024-12-31")).toBe("2025-01-01");
  });

  it("handles leap day", () => {
    expect(dayAfter("2024-02-28")).toBe("2024-02-29");
    expect(dayAfter("2024-02-29")).toBe("2024-03-01");
    expect(dayAfter("2023-02-28")).toBe("2023-03-01");
  });

  it("treats empty and malformed input as an open bound", () => {
    expect(dayAfter("")).toBeUndefined();
    expect(dayAfter(undefined)).toBeUndefined();
    expect(dayAfter("14/03/2024")).toBeUndefined();
  });
});

describe("dayBefore", () => {
  it("steps back one day", () => {
    expect(dayBefore("2024-03-15")).toBe("2024-03-14");
  });

  it("rolls back over month and year boundaries", () => {
    expect(dayBefore("2024-05-01")).toBe("2024-04-30");
    expect(dayBefore("2025-01-01")).toBe("2024-12-31");
    expect(dayBefore("2024-03-01")).toBe("2024-02-29");
  });

  it("inverts dayAfter", () => {
    expect(dayBefore(dayAfter("2024-02-29"))).toBe("2024-02-29");
  });
});

describe("toDayInput / fromDayInput", () => {
  it("shows the local calendar day of an instant", () => {
    expect(toDayInput(new Date(2024, 6, 1))).toBe("2024-07-01");
    expect(toDayInput(new Date(2024, 0, 9))).toBe("2024-01-09");
  });

  it("reads a day back as local midnight", () => {
    expect(fromDayInput("2024-07-01")).toEqual(new Date(2024, 6, 1));
  });

  it("round trips a local calendar date", () => {
    const at = new Date(2024, 11, 31);
    expect(fromDayInput(toDayInput(at))).toEqual(at);
  });

  it("treats empty and malformed input as no date", () => {
    expect(fromDayInput("")).toBeUndefined();
    expect(fromDayInput("01/07/2024")).toBeUndefined();
  });
});
