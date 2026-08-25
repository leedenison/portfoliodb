import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ExchangeSchema, ListingSchema } from "@/gen/api/v1/api_pb";
import {
  lineOf,
  NO_CURRENCY_KNOWN,
  NO_LINE_NAMED,
  NOT_IDENTIFIED,
  venueLabel,
  venuesOf,
  venueTitle,
} from "./listing";

/**
 * A position on no line is unpriced, and which of the three reasons it is
 * decides what a surface can tell the user to do about it. Collapsing them into
 * one "unknown" is the failure the listing level exists to remove.
 */
describe("lineOf", () => {
  const line = (id: string, currency: string) =>
    create(ListingSchema, { id, currency });

  it("takes the currency the message states", () => {
    const inst = { listings: [line("gbp", "GBP")] };
    expect(lineOf("gbp", inst, "GBX")).toEqual({ currency: "GBX", missing: "" });
  });

  it("reads the currency off the named line where the message states none", () => {
    const inst = { listings: [line("gbp", "GBP"), line("usd", "USD")] };
    expect(lineOf("usd", inst)).toEqual({ currency: "USD", missing: "" });
  });

  it("still counts as on a line when the line itself is not in hand", () => {
    // The position is attributed; only the label for it is missing, which is not
    // the same claim as nothing having said which line it is on.
    expect(lineOf("usd", { listings: [] })).toEqual({ currency: "", missing: "" });
  });

  it("says nothing named a line where the security has lines", () => {
    const inst = { listings: [line("gbp", "GBP"), line("usd", "USD")] };
    expect(lineOf("", inst).missing).toBe(NO_LINE_NAMED);
  });

  it("says the security has no line where it holds none", () => {
    expect(lineOf("", { listings: [] }).missing).toBe(NO_CURRENCY_KNOWN);
  });

  it("says the security is unidentified where there is none", () => {
    // A holding known only by its broker description has no security to hold
    // lines, so asking which line it is on is asking the wrong question.
    expect(lineOf("", undefined).missing).toBe(NOT_IDENTIFIED);
  });
});

/**
 * A venue set is what we know about a line, not what exists, so the label shows
 * every venue rather than picking one and never claims to be complete. See
 * docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
 */
describe("venuesOf", () => {
  const venue = (mic: string, acronym: string, name: string) =>
    create(ExchangeSchema, { mic, acronym, name });
  const lineAt = (id: string, venues: ReturnType<typeof venue>[]) =>
    create(ListingSchema, { id, currency: "GBP", venues });

  it("reads the venues off the named line", () => {
    const inst = {
      listings: [
        lineAt("gbp", [venue("XLON", "LSE", "London Stock Exchange")]),
        lineAt("usd", [venue("XNAS", "NASDAQ", "Nasdaq")]),
      ],
    };
    expect(venuesOf("gbp", inst).map((v) => v.mic)).toEqual(["XLON"]);
  });

  it("gives no venues for a position on no line", () => {
    // A venue is a fact about a line, so a position on none has no venue to
    // show rather than an unknown one.
    const inst = { listings: [lineAt("gbp", [venue("XLON", "LSE", "London")])] };
    expect(venuesOf("", inst)).toEqual([]);
  });

  it("gives no venues for a line nobody named one for", () => {
    expect(venuesOf("gbp", { listings: [lineAt("gbp", [])] })).toEqual([]);
  });
});

describe("venueLabel", () => {
  const venue = (mic: string, acronym: string, name: string) =>
    create(ExchangeSchema, { mic, acronym, name });

  it("shows every venue rather than choosing one", () => {
    const venues = [
      venue("XLON", "LSE", "London Stock Exchange"),
      venue("BATE", "", "Cboe Europe"),
    ];
    expect(venueLabel(venues)).toBe("LSE, BATE");
    expect(venueTitle(venues)).toBe("London Stock Exchange, Cboe Europe");
  });

  it("falls back to the MIC where the venue has no shorter name", () => {
    expect(venueLabel([venue("XBOG", "", "")])).toBe("XBOG");
  });

  it("is empty for no venues, which a caller renders as an em-dash", () => {
    expect(venueLabel([])).toBe("");
  });
});
