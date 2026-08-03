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
  session: () => ["session"] as const,
  portfolios: () => ["portfolios"] as const,
  plugins: (category: "identifier" | "description" | "price" | "inflation") =>
    ["plugins", category] as const,
  workers: () => ["workers"] as const,
  unhandledCorporateEvents: () => ["unhandled-corporate-events"] as const,
  unhandledCorporateEventCount: () => ["unhandled-corporate-event-count"] as const,
  corporateEventSplits: () => ["corporate-event-splits"] as const,
  priceFetchBlocks: () => ["price-fetch-blocks"] as const,
  telemetryCounters: () => ["telemetry-counters"] as const,
  holdings: (portfolioId?: string) => ["holdings", portfolioId ?? null] as const,
  displayCurrency: () => ["display-currency"] as const,
  valuation: (portfolioId: string | undefined, dateFrom: string, dateBefore: string, currency: string) =>
    ["valuation", portfolioId ?? null, dateFrom, dateBefore, currency] as const,
  ignoredAssetClasses: () => ["ignored-asset-classes"] as const,
  holdingDeclarations: () => ["holding-declarations"] as const,
  brokersAndAccounts: () => ["brokers-and-accounts"] as const,
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
  // Called with no arguments to invalidate every period variant.
  residualBalances: (periodFrom?: string, periodBefore?: string) =>
    periodFrom === undefined
      ? (["residual-balances"] as const)
      : (["residual-balances", periodFrom, periodBefore ?? ""] as const),
  residualBalanceCounts: () => ["residual-balance-counts"] as const,
};
