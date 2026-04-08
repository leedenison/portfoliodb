/**
 * Stock split ratio helpers used by the admin Corporate Events page.
 *
 * Conventions:
 *   factor = split_to / split_from
 *   For a "20 for 1" split: split_from = "1", split_to = "20", factor = 20.
 */

export interface RatioValidation {
  ok: boolean;
  /** Hard error: blocks submission. */
  error?: string;
  /** Soft warning: allows submission. */
  warning?: string;
}

/**
 * Validate a split ratio. Both fields must parse as positive finite
 * numbers and neither may be zero. A factor outside [0.01, 100] yields a
 * non-blocking warning.
 */
export function validateRatio(splitFrom: string | undefined, splitTo: string | undefined): RatioValidation {
  if (!splitFrom || !splitTo) {
    return { ok: false, error: "Both split_from and split_to are required" };
  }
  const from = parseFloat(splitFrom);
  const to = parseFloat(splitTo);
  if (!Number.isFinite(from) || !Number.isFinite(to)) {
    return { ok: false, error: "split_from and split_to must be numbers" };
  }
  if (from <= 0 || to <= 0) {
    return { ok: false, error: "split_from and split_to must be positive" };
  }
  const factor = to / from;
  if (!Number.isFinite(factor)) {
    return { ok: false, error: "Invalid ratio" };
  }
  if (factor > 100 || factor < 0.01) {
    return { ok: true, warning: `Unusual ratio (factor ${factor.toFixed(2)})` };
  }
  return { ok: true };
}

export interface PrefillResult {
  /** Suggested split_from / split_to as decimal strings, or null if no sane value. */
  splitFrom: string | null;
  splitTo: string | null;
}

/**
 * Infer a split ratio from a pre-split position and the share delta
 * reported on the broker SPLIT row. Returns null fields when the result
 * would be nonsensical (non-positive position, non-positive post
 * position, non-finite ratio).
 *
 * Mathematically: factor = (pre + delta) / pre. We always normalize as
 * split_from = "1", split_to = factor.
 */
export function prefillFromPosition(pre: number, delta: number): PrefillResult {
  if (!Number.isFinite(pre) || !Number.isFinite(delta)) {
    return { splitFrom: null, splitTo: null };
  }
  if (pre <= 0) return { splitFrom: null, splitTo: null };
  const post = pre + delta;
  if (post <= 0) return { splitFrom: null, splitTo: null };
  const factor = post / pre;
  if (!Number.isFinite(factor) || factor <= 0) {
    return { splitFrom: null, splitTo: null };
  }
  // Trim to a reasonable precision; integers stay clean.
  const rounded = Math.round(factor * 1e6) / 1e6;
  return { splitFrom: "1", splitTo: String(rounded) };
}
