import { describe, expect, it } from "vitest";
import { formatDate, parseSlashDate } from "./dates";

describe("formatDate", () => {
  it("pads day and month", () => {
    expect(formatDate(new Date(2026, 6, 2), "dd/MM/yyyy")).toBe("02/07/2026");
    expect(formatDate(new Date(2026, 11, 27), "dd/MM/yyyy")).toBe("27/12/2026");
  });

  it("supports a two-digit year", () => {
    expect(formatDate(new Date(2026, 0, 5), "dd/MM/yy")).toBe("05/01/26");
  });

  it("reads local date parts, matching how converters build timestamps", () => {
    // Built from local midnight, so the calendar day must survive regardless of
    // the runtime's offset from UTC.
    const d = new Date(2026, 0, 1);
    expect(formatDate(d, "yyyy-MM-dd")).toBe("2026-01-01");
  });
});

describe("parseSlashDate", () => {
  it("parses to local midnight", () => {
    expect(parseSlashDate("02/07/2026")).toEqual(new Date(2026, 6, 2));
  });

  it("rejects a date that does not exist", () => {
    // Date would silently roll 31 February into March.
    expect(parseSlashDate("31/02/2026")).toBeNull();
    expect(parseSlashDate("00/07/2026")).toBeNull();
    expect(parseSlashDate("02/13/2026")).toBeNull();
  });

  it("rejects malformed input", () => {
    for (const s of ["", "2026-07-02", "2/7/2026", "02/07/26", "not a date"]) {
      expect(parseSlashDate(s)).toBeNull();
    }
  });
});
