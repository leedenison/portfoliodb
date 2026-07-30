import { describe, it, expect } from "vitest";
import { dayAfter, dayBefore } from "./dates";

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
