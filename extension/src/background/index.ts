/**
 * Service worker entry point.
 *
 * Sync orchestration, the API clients and the session bootstrap arrive in later
 * changes; this exists so the manifest has a background entry to load and so the
 * build emits it.
 */

chrome.runtime.onInstalled.addListener(() => {
  console.info("PortfolioDB Import installed");
});
