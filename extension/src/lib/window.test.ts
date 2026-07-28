import { describe, expect, it } from "vitest";
import { computeWindow, type WindowInputs } from "./window";

const base: WindowInputs = {
  latest: null,
  historyStartDate: "",
  lookbackDays: 14,
  timeZone: "Europe/London",
  now: new Date("2026-07-27T09:00:00Z"),
};

/** Narrows to the window case so tests can read from/to without repeating guards. */
function window(inputs: Partial<WindowInputs>) {
  const result = computeWindow({ ...base, ...inputs });
  if (result.kind !== "window") throw new Error(`expected a window, got ${result.kind}`);
  return result;
}

describe("computeWindow", () => {
  it("ends at the end of yesterday", () => {
    // A transaction dated today may not have completed, so today is excluded.
    const { to } = window({ latest: new Date(2026, 6, 20) });
    expect(to).toEqual(new Date(2026, 6, 26, 23, 59, 59, 999));
  });

  it("starts a lookback before the last known transaction", () => {
    const { from } = window({ latest: new Date(2026, 6, 20), lookbackDays: 14 });
    expect(from).toEqual(new Date(2026, 6, 6, 0, 0, 0, 0));
  });

  it("overlaps rather than resuming, so a re-dated transaction cannot duplicate", () => {
    // A row ordered on the 6th and still pending would have been stored under the
    // order date. The window must reach back far enough to replace it once it
    // settles under a later completion date.
    const orderDate = new Date(2026, 6, 6);
    const { from } = window({ latest: new Date(2026, 6, 20), lookbackDays: 14 });
    expect(from.getTime()).toBeLessThanOrEqual(orderDate.getTime());
  });

  it("uses the history start date when the broker has no transactions", () => {
    const { from } = window({ latest: null, historyStartDate: "2024-01-15" });
    expect(from).toEqual(new Date(2024, 0, 15, 0, 0, 0, 0));
  });

  it("never reaches back past the history start date", () => {
    // The lookback would land in 2023; the declared start of history wins.
    const { from } = window({
      latest: new Date(2024, 0, 20),
      historyStartDate: "2024-01-15",
      lookbackDays: 400,
    });
    expect(from).toEqual(new Date(2024, 0, 15, 0, 0, 0, 0));
  });

  it("errors when there is nothing to resume from and no start date", () => {
    const result = computeWindow({ ...base, latest: null, historyStartDate: "" });
    expect(result.kind).toBe("error");
    if (result.kind === "error") expect(result.message).toContain("history start date");
  });

  it("reports up to date when the last transaction is already past yesterday", () => {
    const result = computeWindow({
      ...base,
      latest: new Date(2026, 6, 27),
      lookbackDays: 0,
    });
    expect(result.kind).toBe("up-to-date");
  });

  it("rejects a malformed history start date rather than guessing", () => {
    const result = computeWindow({ ...base, historyStartDate: "15/01/2024" });
    expect(result.kind).toBe("error");
  });

  it("rejects an unknown time zone", () => {
    const result = computeWindow({ ...base, latest: new Date(2026, 6, 20), timeZone: "Mars/Olympus" });
    expect(result.kind).toBe("error");
  });

  describe("the configured zone selects the calendar day", () => {
    it("treats a UTC instant that is already tomorrow in the zone", () => {
      // 23:30 UTC on the 26th is 00:30 on the 27th in Auckland's summer, so
      // yesterday there is the 26th, not the 25th.
      const { to } = window({
        latest: new Date(2026, 0, 1),
        timeZone: "Pacific/Auckland",
        now: new Date("2026-01-26T23:30:00Z"),
      });
      expect(to.getDate()).toBe(26);
    });

    it("treats a UTC instant that is still yesterday in the zone", () => {
      // 00:30 UTC on the 27th is 19:30 on the 26th in New York, so yesterday
      // there is the 25th.
      const { to } = window({
        latest: new Date(2026, 0, 1),
        timeZone: "America/New_York",
        now: new Date("2026-01-27T00:30:00Z"),
      });
      expect(to.getDate()).toBe(25);
    });
  });

  it("builds bounds at local midnight, matching how converters date rows", () => {
    // Converters call new Date(y, m, d), so a bound built in another zone would
    // not bracket the rows it is meant to cover.
    const { from, to } = window({ latest: new Date(2026, 6, 20) });
    expect(from.getHours()).toBe(0);
    expect(from.getMinutes()).toBe(0);
    expect(to.getHours()).toBe(23);
    expect(to.getMilliseconds()).toBe(999);
  });
});
