import { describe, it, expect } from "vitest";
import { defaultVintage, uploadVintage } from "@/lib/upload-vintage";

describe("defaultVintage", () => {
  it("believes a file that dates itself", () => {
    const stated = new Date("2026-04-05T14:57:30Z");
    expect(defaultVintage(stated, new Date(2026, 3, 4))).toBe(stated);
  });

  // An export cannot precede the transactions it describes, so the last day the
  // window covers is the earliest date the file could honestly claim -- and a
  // better answer than today for a file that has been sitting on disk.
  it("falls back to the last day the window covers", () => {
    expect(defaultVintage(undefined, new Date(2026, 3, 4))).toEqual(new Date(2026, 3, 3));
  });

  it("states nothing when there is neither", () => {
    expect(defaultVintage(undefined, undefined)).toBeUndefined();
  });
});

describe("uploadVintage", () => {
  // Quantising a stated instant to the day it was displayed as would move it by
  // a day west of Greenwich, and the day either side of an ex_date is exactly
  // what this value decides.
  it("sends a stated instant whole while the field is untouched", () => {
    const stated = new Date("2026-04-05T14:57:30Z");
    expect(uploadVintage({ stated, edited: null })).toBe(stated);
  });

  it("sends the day the user picked once the field is edited", () => {
    expect(
      uploadVintage({ stated: new Date("2026-04-05T14:57:30Z"), edited: "2024-07-01" }),
    ).toEqual(new Date(2024, 6, 1));
  });

  // Clearing the field says "I do not know", which is the server taking the
  // upload for the export rather than a date this side invents.
  it("sends nothing for a cleared field", () => {
    expect(uploadVintage({ stated: new Date("2026-04-05T14:57:30Z"), edited: "" })).toBeUndefined();
  });

  it("sends the fallback when the file states nothing", () => {
    expect(uploadVintage({ periodBefore: new Date(2026, 3, 4), edited: null })).toEqual(
      new Date(2026, 3, 3),
    );
  });
});
