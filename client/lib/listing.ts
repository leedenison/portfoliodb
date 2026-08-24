/**
 * What currency line a position is on, and what to say when it is on none.
 *
 * A holding is per line, because two lines of one security are an FX rate apart
 * and adding them reports a number in no currency at all. A position the system
 * cannot attribute to a line is real and its quantity is right; what is unknown
 * is what it is denominated in, so it is reported unpriced rather than valued at
 * a rate nobody stated. See docs/spec/display-currency.md and
 * docs/adr/0072-a-posting-names-a-security-and-a-line.md.
 *
 * Two shapes reach that state and they are different questions -- nothing said
 * which line this is, against this security has no line to be on -- so a surface
 * reporting one says which. The words are declared here rather than at each call
 * site so the holdings row and the admin count that totals them read the same.
 */

import type { Listing } from "@/gen/api/v1/api_pb";

/** The security is quoted in more than one currency and no posting said which. */
export const NO_LINE_NAMED = "No line named";

/** Nothing has named a currency line for the security, so it has none. */
export const NO_CURRENCY_KNOWN = "No currency known";

/** Identification has not resolved the security, so the question has not arisen. */
export const NOT_IDENTIFIED = "Not identified";

/** Why a position cannot be valued, spelled out for a title or a tooltip. */
export const LINE_DETAIL: Record<string, string> = {
  [NO_LINE_NAMED]:
    "No transaction said which currency line this position is on, so it cannot be priced.",
  [NO_CURRENCY_KNOWN]:
    "Nothing has named a currency line for this security, so there is no price series to value it from.",
  [NOT_IDENTIFIED]:
    "The security has not been identified, so it has no currency lines yet.",
};

/**
 * The line a position sits on: its currency, or which of the three the missing
 * line is. Exactly one of the two is set.
 */
export interface Line {
  /** The code the position is quoted in; empty when it is on no line. */
  currency: string;
  /** One of the phrases above; empty when the position is on a line. */
  missing: string;
}

/**
 * The line a holding or a declaration names.
 *
 * `currency` is passed where the message carries one of its own -- a holding
 * does -- and looked up on the security's lines otherwise. A listing id with no
 * currency to be found is still a line: the position is attributed, and only the
 * label for it is missing.
 */
export function lineOf(
  listingId: string,
  inst: { listings?: Listing[] } | undefined,
  currency?: string,
): Line {
  if (listingId) {
    const found = inst?.listings?.find((l) => l.id === listingId)?.currency;
    return { currency: currency || found || "", missing: "" };
  }
  if (!inst) return { currency: "", missing: NOT_IDENTIFIED };
  return {
    currency: "",
    missing: inst.listings?.length ? NO_LINE_NAMED : NO_CURRENCY_KNOWN,
  };
}
