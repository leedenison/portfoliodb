/**
 * Query keys, in one place so that invalidation and the queries it targets cannot
 * drift apart.
 *
 * Two conventions:
 *
 *   - The first element is the resource name and the rest are its parameters, so
 *     `invalidateQueries({ queryKey: qk.prices() })` prefix-matches every filter
 *     and page variant of it.
 *   - Parameters are primitives, never objects. Keys are compared structurally,
 *     so an object that is rebuilt with the same contents is a different key and
 *     silently refetches. Pass `portfolio.id`, not `portfolio`.
 *
 * Grows as call sites are converted; there are no keys here without a consumer.
 */
export const qk = {
  telemetryCounters: () => ["telemetry-counters"] as const,
};
