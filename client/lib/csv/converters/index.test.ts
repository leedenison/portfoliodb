/**
 * Registration happens as a side effect of importing the barrel. Splitting a
 * converter into a pure module plus a registering module is easy to get wrong in
 * a way that silently drops a broker from the upload dropdown, so assert the
 * registry is populated rather than trusting the import chain.
 */

import { describe, it, expect } from "vitest";
import { Broker } from "@/gen/api/v1/api_pb";
import {
  getBrokerEntry,
  getBrokerOptionsForUpload,
  getFormatsForBroker,
  getSourcePrefix,
} from "./index";

describe("converter registry", () => {
  it("offers Fidelity in the upload broker list", () => {
    expect(getBrokerOptionsForUpload()).toContainEqual({
      value: Broker.FIDELITY,
      label: "Fidelity",
    });
  });

  it("registers the Fidelity CSV format with a converter and an options component", () => {
    const format = getBrokerEntry(Broker.FIDELITY)?.formats.find((f) => f.id === "fidelity-csv");
    expect(format).toBeDefined();
    expect(format!.convert).toBeTypeOf("function");
    expect(format!.OptionsComponent).toBeTypeOf("function");
  });

  it("builds the Fidelity source string the ingestion API expects", () => {
    // Matches the source used by the upload modal, and reused by the extension:
    // it is the instrument-resolution cache key, so it must not drift.
    expect(`${getSourcePrefix(Broker.FIDELITY)}:web:fidelity-csv`).toBe("Fidelity:web:fidelity-csv");
  });

  it("offers Standard alongside the broker formats", () => {
    expect(getFormatsForBroker(Broker.FIDELITY).map((f) => f.id)).toEqual(["standard", "fidelity-csv"]);
  });
});
