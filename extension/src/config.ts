/**
 * Extension configuration, persisted in chrome.storage.local.
 *
 * See docs/spec/broker-import-extension.md for what each setting means to a sync
 * run.
 */

export interface Config {
  /** Base URL of the PortfolioDB deployment, e.g. "http://localhost:8080". */
  portfoliodbOrigin: string;
  /** Settlement currency passed to converters that require one. */
  currency: string;
  /** "YYYY-MM-DD" window start for the first sync, when no transactions exist yet. */
  historyStartDate: string;
  /**
   * Days of overlap before the last known transaction. Must exceed the broker's
   * longest order-to-completion lag: a transaction that was Pending at export
   * time is dated by its order date and re-dated later when it settles, so an
   * order date falling outside the window survives the replace and duplicates.
   */
  lookbackDays: number;
  /** Determines which calendar day counts as "yesterday" when sizing the window. */
  timeZone: string;
}

export const DEFAULT_CONFIG: Config = {
  portfoliodbOrigin: "",
  currency: "",
  historyStartDate: "",
  lookbackDays: 14,
  timeZone: "Europe/London",
};

const STORAGE_KEY = "config";

export async function loadConfig(): Promise<Config> {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  return { ...DEFAULT_CONFIG, ...(stored[STORAGE_KEY] as Partial<Config> | undefined) };
}

/** Merge a partial update over the stored config and persist it. */
export async function saveConfig(patch: Partial<Config>): Promise<Config> {
  const next = { ...(await loadConfig()), ...patch };
  await chrome.storage.local.set({ [STORAGE_KEY]: next });
  return next;
}

/**
 * Settings a sync run cannot proceed without. Reported up front rather than
 * failing mid-run, and in particular historyStartDate is only required when the
 * user has no transactions for the broker yet -- which the popup cannot know, so
 * it is reported as advisory here and enforced by the sync itself.
 */
export function missingRequired(config: Config): (keyof Config)[] {
  const missing: (keyof Config)[] = [];
  if (!config.portfoliodbOrigin) missing.push("portfoliodbOrigin");
  if (!config.currency) missing.push("currency");
  return missing;
}
