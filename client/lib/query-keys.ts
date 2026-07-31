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
  // Called with no arguments to invalidate every search/filter variant.
  instruments: (search?: string, assetClasses?: string) =>
    search === undefined ? (["instruments"] as const) : (["instruments", search, assetClasses ?? ""] as const),
  // Called with no arguments to invalidate every filter variant.
  prices: (search?: string, dateFrom?: string, dateTo?: string) =>
    search === undefined
      ? (["prices"] as const)
      : (["prices", search, dateFrom ?? "", dateTo ?? ""] as const),
  txs: (portfolioId?: string) => ["txs", portfolioId ?? null] as const,
  jobs: () => ["jobs"] as const,
  job: (jobId: string) => ["job", jobId] as const,
  inflationIndices: (currency: string, dateFrom: string, dateTo: string) =>
    ["inflation-indices", currency, dateFrom, dateTo] as const,
};
