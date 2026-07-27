/**
 * Storage for the PortfolioDB session id.
 *
 * Held in chrome.storage.session rather than storage.local: the id is a live
 * credential, and session storage is in-memory and cleared when the browser
 * restarts. The cost is re-running the bootstrap once per browser session, which
 * is a single click against an already-signed-in tab.
 */

const STORAGE_KEY = "sessionId";

export async function getSessionId(): Promise<string> {
  const stored = await chrome.storage.session.get(STORAGE_KEY);
  const id = stored[STORAGE_KEY];
  return typeof id === "string" ? id : "";
}

export async function setSessionId(sessionId: string): Promise<void> {
  await chrome.storage.session.set({ [STORAGE_KEY]: sessionId });
}

export async function clearSessionId(): Promise<void> {
  await chrome.storage.session.remove(STORAGE_KEY);
}
