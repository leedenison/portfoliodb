/** Identifier helpers shared by the import parsers. */

import { IdentifierType, IdentifierTypeSchema } from "@/gen/type/v1/type_pb";
import type { InstrumentIdentifier, Listing } from "@/gen/api/v1/api_pb";

/** Valid identifier type names from the proto IdentifierType enum (excluding UNSPECIFIED). */
export const VALID_IDENTIFIER_TYPES = new Set(
  IdentifierTypeSchema.values
    .filter((v) => v.number !== 0)
    .map((v) => v.name),
);

/** Every name a security answers to, whichever grain it sits at. */
type Named = {
  identifiers?: InstrumentIdentifier[];
  unplacedIdentifiers?: InstrumentIdentifier[];
  listings?: Listing[];
};

/**
 * The ticker an instrument answers to now, or undefined if it has none.
 *
 * An identifier carries the half-open interval its name was correct for, so an
 * instrument can hold a ticker it has since given up alongside the one it wears
 * now. A label must show the current one; an absent `validBefore` is what marks
 * it. See docs/adr/0055-identifier-validity-is-an-interval.md.
 *
 * A ticker names one currency line rather than the security above it, so a
 * caller that knows which line it means passes it and gets that line's ticker:
 * the GBP and USD lines of one security wear different symbols, and showing
 * either for the other is showing a name for a line the row is not on.
 *
 * With no line named -- a transaction row, a search result, a security whose
 * lines are not in hand -- every line's ticker is a name this security answers
 * to and any of them labels it, so the search widens rather than returning
 * nothing. That is the answer for a caller that has not picked a grain, and it
 * cannot say which line the one it found belongs to.
 */
export function currentTicker(
  inst: Named | undefined,
  listingId?: string,
): string | undefined {
  if (!inst) return undefined;
  if (listingId) {
    const line = inst.listings?.find((l) => l.id === listingId);
    return tickerIn(line?.identifiers);
  }
  return (
    tickerIn(inst.identifiers) ??
    inst.listings?.map((l) => tickerIn(l.identifiers)).find((t) => t) ??
    tickerIn(inst.unplacedIdentifiers)
  );
}

/** The ticker in force among one grain's identifiers. */
function tickerIn(ids: InstrumentIdentifier[] | undefined): string | undefined {
  return ids?.find(
    (id) =>
      !id.validBefore &&
      (id.type === IdentifierType.MIC_TICKER ||
        id.type === IdentifierType.OPENFIGI_TICKER),
  )?.value;
}

/**
 * A label with the currency line it is on -- "VOD (GBP)".
 *
 * The currency is what discloses a line, rather than the venue: two lines of one
 * security differ by an FX rate, which is what a user reading a holding needs to
 * know, and a MIC tells them nothing they can act on. Where no line is in hand
 * the label stands alone; a surface that means to say a line is missing says so
 * itself rather than leaving a bare label to imply it.
 */
export function lineLabel(label: string, currency: string | undefined): string {
  return currency ? `${label} (${currency})` : label;
}
