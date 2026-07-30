/**
 * Works out which period a sync should fetch.
 *
 * Two things make this less obvious than "everything since the last transaction".
 *
 * The window deliberately overlaps what is already held. A broker row that has
 * not settled is dated by its order date and re-dated to its completion date once
 * it does, so resuming after the last known transaction would leave the earlier
 * copy outside the replace window, where it survives and duplicates. The lookback
 * must therefore exceed the broker's longest order-to-completion lag.
 *
 * The configured timezone decides only which calendar day counts as "yesterday".
 * The bounds are then built as local midnights, because that is how the
 * converters build transaction timestamps; constructing them in a different
 * zone would shift the boundary by up to a day.
 */

import { parseIsoDate } from "./dates";

export interface WindowInputs {
  /** Timestamp of the most recent transaction already held for this broker. */
  latest: Date | null;
  /** "yyyy-MM-dd"; required when there is no latest transaction. */
  historyStartDate: string;
  lookbackDays: number;
  timeZone: string;
  /** Injected so the result is testable. */
  now: Date;
}

export type WindowResult =
  /** Half-open [from, before): local midnights, `before` exclusive. */
  | { kind: "window"; from: Date; before: Date }
  | { kind: "up-to-date" }
  | { kind: "error"; message: string };

/** The calendar date at an instant in a given timezone, as [year, month, day]. */
function calendarDay(at: Date, timeZone: string): [number, number, number] {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(at);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  return [get("year"), get("month"), get("day")];
}

function startOfDay(y: number, m: number, d: number): Date {
  return new Date(y, m - 1, d, 0, 0, 0, 0);
}

function minusDays(date: Date, days: number): Date {
  const out = new Date(date);
  out.setDate(out.getDate() - days);
  return out;
}

export function computeWindow(inputs: WindowInputs): WindowResult {
  const { latest, historyStartDate, lookbackDays, timeZone, now } = inputs;

  let today: [number, number, number];
  try {
    today = calendarDay(now, timeZone);
  } catch {
    return { kind: "error", message: `Unknown time zone: ${timeZone}` };
  }
  if (Number.isNaN(today[0])) {
    return { kind: "error", message: `Unknown time zone: ${timeZone}` };
  }
  // The window runs up to but not including today: today's rows are still
  // arriving. The calendar day comes from the configured zone, then is
  // materialised locally like every other bound here.
  const before = startOfDay(...today);

  const historyStart = historyStartDate ? parseIsoDate(historyStartDate) : null;
  if (historyStartDate && !historyStart) {
    return { kind: "error", message: `History start date is not a date: ${historyStartDate}` };
  }

  let from: Date;
  if (latest) {
    const overlapped = minusDays(latest, Math.max(0, lookbackDays));
    const lower = startOfDay(
      overlapped.getFullYear(),
      overlapped.getMonth() + 1,
      overlapped.getDate()
    );
    // Never reach back past the declared start of history, even if the lookback
    // would.
    from = historyStart && historyStart > lower ? historyStart : lower;
  } else if (historyStart) {
    from = historyStart;
  } else {
    return {
      kind: "error",
      message:
        "No transactions for this broker yet, so there is nothing to resume from. Set a history start date in settings.",
    };
  }

  if (from >= before) return { kind: "up-to-date" };
  return { kind: "window", from, before };
}
