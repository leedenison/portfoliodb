import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ListingSchema } from "@/gen/api/v1/api_pb";
import {
  lineOf,
  NO_CURRENCY_KNOWN,
  NO_LINE_NAMED,
  NOT_IDENTIFIED,
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
