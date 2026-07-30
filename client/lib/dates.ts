/**
 * Conversions between the inclusive dates people type into a date picker and
 * the half-open [from, before) intervals the API speaks.
 *
 * Everything here is calendar arithmetic on "YYYY-MM-DD" strings done through
 * Date.UTC, so the answer never depends on the viewer's timezone: a browser in
 * UTC-5 must send the same bound as one in UTC+9 for the same typed date.
 */

const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;

function shift(date: string | undefined, days: number): string | undefined {
  if (!date) return undefined;
  const m = ISO_DATE.exec(date.trim());
  if (!m) return undefined;
  const shifted = new Date(
    Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + days)
  );
  return shifted.toISOString().slice(0, 10);
}

/**
 * The exclusive bound covering an inclusive last day: what to send for a date
 * the user means to include. Returns undefined for an empty or malformed input,
 * which the API treats as an open-ended bound.
 */
export function dayAfter(date?: string): string | undefined {
  return shift(date, 1);
}

/** The inclusive last day an exclusive bound covers, for display. */
export function dayBefore(date?: string): string | undefined {
  return shift(date, -1);
}
