/** Identifier helpers shared by the import parsers. */

import { IdentifierType, IdentifierTypeSchema } from "@/gen/type/v1/type_pb";
import type { InstrumentIdentifier } from "@/gen/api/v1/api_pb";

/** Valid identifier type names from the proto IdentifierType enum (excluding UNSPECIFIED). */
export const VALID_IDENTIFIER_TYPES = new Set(
  IdentifierTypeSchema.values
    .filter((v) => v.number !== 0)
    .map((v) => v.name),
);

/**
 * The ticker an instrument answers to now, or undefined if it has none.
 *
 * An identifier carries the half-open interval its name was correct for, so an
 * instrument can hold a ticker it has since given up alongside the one it wears
 * now. A label must show the current one; an absent `validBefore` is what marks
 * it. See docs/adr/0055-identifier-validity-is-an-interval.md.
 */
export function currentTicker(
  inst: { identifiers?: InstrumentIdentifier[] } | undefined,
): string | undefined {
  return inst?.identifiers?.find(
    (id) =>
      !id.validBefore &&
      (id.type === IdentifierType.MIC_TICKER ||
        id.type === IdentifierType.OPENFIGI_TICKER),
  )?.value;
}
