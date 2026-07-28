/**
 * Date formatting and parsing for broker payloads.
 *
 * Everything here works in the runtime's local timezone, deliberately. The CSV
 * and JSON converters build transaction timestamps from local midnight, so a
 * window bound built in a different timezone would not bracket the rows it is
 * meant to cover.
 */

/** Formats a date using the tokens yyyy, yy, MM and dd. */
export function formatDate(date: Date, pattern: string): string {
  const pad = (n: number, width = 2) => String(n).padStart(width, "0");
  return pattern
    .replace(/yyyy/g, pad(date.getFullYear(), 4))
    .replace(/yy/g, pad(date.getFullYear() % 100))
    .replace(/MM/g, pad(date.getMonth() + 1))
    .replace(/dd/g, pad(date.getDate()));
}

/**
 * Parses "yyyy-MM-dd" to local midnight, as date inputs and stored settings spell
 * dates. Returns null if malformed -- deliberately strict, so a date in another
 * format is rejected rather than reinterpreted.
 */
export function parseIsoDate(value: string): Date | null {
  const m = value.trim().match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (!m) return null;
  return parseSlashDate(`${m[3]}/${m[2]}/${m[1]}`);
}

/** Parses "dd/MM/yyyy" to local midnight. Returns null if malformed. */
export function parseSlashDate(value: string): Date | null {
  const m = value.trim().match(/^(\d{2})\/(\d{2})\/(\d{4})$/);
  if (!m) return null;
  const [, dd, mm, yyyy] = m;
  const day = parseInt(dd!, 10);
  const month = parseInt(mm!, 10);
  const year = parseInt(yyyy!, 10);
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  const date = new Date(year, month - 1, day);
  // Rejects overflow such as 31/02/2026, which Date would roll into March.
  if (date.getFullYear() !== year || date.getMonth() !== month - 1 || date.getDate() !== day) {
    return null;
  }
  return date;
}
