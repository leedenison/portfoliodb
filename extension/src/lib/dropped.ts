/**
 * Summarising the rows a converter refused.
 *
 * A dropped row is not merely skipped. Ingestion replaces the period wholesale,
 * so any stored copy of that row inside the window is deleted and not put back.
 * Uploading regardless keeps a sync usable when a broker introduces a transaction
 * type, but the run has to say loudly what it gave up, and name the types
 * precisely enough to extend the converter's map.
 */

import type { ParseError } from "@/lib/csv/standard";

/**
 * Distinct broker transaction types the converter did not recognise.
 *
 * Read back out of the parse errors rather than rescanning the payload, so this
 * cannot disagree with the rows the converter actually dropped.
 */
export function droppedTypes(errors: ParseError[]): string[] {
  const types = new Set<string>();
  for (const e of errors) {
    if (e.field !== "type") continue;
    const m = e.message.match(/Unknown transaction type: (.+)$/);
    if (m?.[1]) types.add(m[1]);
  }
  return [...types].sort();
}

/** One line summarising what was dropped, or null when nothing was. */
export function droppedSummary(errors: ParseError[]): string | null {
  if (errors.length === 0) return null;
  const types = droppedTypes(errors);
  const rows = `${errors.length} row${errors.length === 1 ? "" : "s"} dropped`;
  return types.length > 0 ? `${rows}: ${types.join(", ")}` : rows;
}
