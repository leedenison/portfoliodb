/**
 * The registry itself, with entries this file puts in it.
 *
 * index.test.ts asserts the real registry ends up populated, which is the thing
 * that silently breaks when a converter is split into a pure module and a
 * registering one. This is the other half: what the lookups do, and what they do
 * for a broker nobody registered. Vitest gives each test file its own module
 * graph, so importing the registry here rather than the barrel gets an empty one
 * with no converters registered into it.
 */

import { beforeAll, describe, expect, it } from "vitest";
import { Broker } from "@/gen/type/v1/type_pb";
import type { StandardParseResult } from "@/lib/csv/parse-result";
import {
  getBrokerEntry,
  getBrokerLabel,
  getBrokerOptionsForUpload,
  getFormatsForBroker,
  getSourcePrefix,
  register,
} from "./registry";

const convert = (): StandardParseResult => ({
  postings: [],
  periodFrom: new Date(0),
  periodBefore: new Date(0),
  errors: [],
});

beforeAll(() => {
  register({
    broker: Broker.FIDELITY,
    label: "Convertible",
    sourcePrefix: "Conv",
    formats: [
      { id: "csv", label: "CSV", accept: ".csv", convert },
      { id: "ofx", label: "OFX", accept: ".ofx,.qfx", convert },
    ],
  });
  // A broker the app knows about but cannot read a file for. This is the real
  // shape of SCHB, which has an entry and no converter -- see
  // docs/issues/0073-schwab-client-converter.md.
  register({
    broker: Broker.SCHB,
    label: "Unconvertible",
    sourcePrefix: "Unconv",
    formats: [{ id: "someday", label: "Someday", accept: ".csv" }],
  });
});

describe("getBrokerOptionsForUpload", () => {
  it("offers a broker that can convert something", () => {
    expect(getBrokerOptionsForUpload()).toContainEqual({
      value: Broker.FIDELITY,
      label: "Convertible",
    });
  });

  it("leaves out a broker with no converter", () => {
    // The dropdown is what a user picks before choosing a file, so a broker whose
    // formats all lack a convert function would offer a path that dead-ends.
    expect(getBrokerOptionsForUpload().map((o) => o.value)).not.toContain(Broker.SCHB);
  });
});

describe("getFormatsForBroker", () => {
  it("puts the archive document ahead of the broker's own formats", () => {
    // Every broker can be handed an archive document, whether or not it has a
    // converter, because the file is read rather than converted.
    expect(getFormatsForBroker(Broker.FIDELITY).map((f) => f.id)).toEqual(["archive", "csv", "ofx"]);
  });

  it("gives the archive entry no converter and a .json accept", () => {
    const archive = getFormatsForBroker(Broker.FIDELITY)[0];
    expect(archive.convert).toBeUndefined();
    expect(archive.accept).toBe(".json");
  });

  it("keeps each format's own accept", () => {
    const ofx = getFormatsForBroker(Broker.FIDELITY).find((f) => f.id === "ofx");
    expect(ofx?.accept).toBe(".ofx,.qfx");
  });

  it("offers the archive document even where nothing can be converted", () => {
    expect(getFormatsForBroker(Broker.SCHB).map((f) => f.id)).toEqual(["archive", "someday"]);
  });

  it("returns nothing for a broker nobody registered", () => {
    expect(getFormatsForBroker(Broker.IBKR)).toEqual([]);
  });
});

describe("the lookups for a broker nobody registered", () => {
  // Each falls back rather than throwing: these are read while rendering, and a
  // broker can reach the UI from stored data before its converter exists.
  it("has no entry", () => {
    expect(getBrokerEntry(Broker.IBKR)).toBeUndefined();
  });

  it("labels it with a dash rather than the enum number", () => {
    expect(getBrokerLabel(Broker.IBKR)).toBe("—");
  });

  it("gives it a source prefix that names nothing", () => {
    // The prefix is part of the source string an instrument resolves against, so
    // a placeholder is better than an empty one: it cannot collide with a real
    // broker's and it is legible in stored data.
    expect(getSourcePrefix(Broker.IBKR)).toBe("unknown");
  });
});

describe("the lookups for a registered broker", () => {
  it("returns the entry it was given", () => {
    expect(getBrokerEntry(Broker.FIDELITY)?.label).toBe("Convertible");
  });

  it("returns its label and source prefix", () => {
    expect(getBrokerLabel(Broker.FIDELITY)).toBe("Convertible");
    expect(getSourcePrefix(Broker.FIDELITY)).toBe("Conv");
  });
});
