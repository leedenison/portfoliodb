/**
 * What an upload states as the vintage of its file's identifiers.
 *
 * A file names an option by the symbol current when it was written, so this is
 * what dates the names a resolution writes from the file's hints. Getting it
 * wrong puts a name on the wrong side of a split's ex_date: dated too early it
 * is restated by a split it already carries, dated too late it misses one it
 * does not. See docs/spec/bitemporality.md.
 *
 * Kept out of the modal so the rule can be read and tested on its own.
 */

import { fromDayInput, lastCoveredDay } from "@/lib/dates";

export interface VintageInput {
  /**
   * What the file says about its own export: an archive envelope's exported_at,
   * or an OFX statement's DTSERVER. Absent for a format that says nothing.
   */
  stated?: Date;
  /** The window's exclusive upper bound, when the file has one. */
  periodBefore?: Date;
  /** The day the user picked, or null while the field is untouched. */
  edited?: string | null;
}

/**
 * The date to show in the upload's vintage field.
 *
 * A file that dates itself is believed. One that does not falls back to the last
 * day its window covers: an export cannot precede the transactions it describes,
 * so that is the earliest date the file could honestly claim, and it beats today
 * for a file that has been sitting on disk. Undefined when there is neither,
 * which leaves the server to take the upload for the export.
 */
export function defaultVintage(stated?: Date, periodBefore?: Date): Date | undefined {
  return stated ?? (periodBefore ? lastCoveredDay(periodBefore) : undefined);
}

/**
 * The vintage to send.
 *
 * An untouched field sends the instant the file stated, whole, rather than the
 * local day it was displayed as -- quantising a stated instant would move it by a
 * day for anyone west of Greenwich, and the day either side of a split's ex_date
 * is exactly what this value decides. An edit sends the day that was picked,
 * because a day is all the user gave.
 */
export function uploadVintage({ stated, periodBefore, edited }: VintageInput): Date | undefined {
  if (edited != null) return fromDayInput(edited);
  return defaultVintage(stated, periodBefore);
}
