/**
 * Drives the session bootstrap: get permission for the configured origin, get a
 * tab on it, inject the bootstrap content script, and wait for the session id it
 * sends back.
 */

import { loadConfig } from "../config";
import type { SessionBootstrapped } from "../lib/messages";

const CONTENT_SCRIPT = "content/portfoliodb.js";
const BOOTSTRAP_TIMEOUT_MS = 15_000;

/** Resolves with the next session-bootstrapped message, or rejects on timeout. */
function awaitSession(timeoutMs = BOOTSTRAP_TIMEOUT_MS): Promise<SessionBootstrapped> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      chrome.runtime.onMessage.removeListener(listener);
      reject(new Error("timed out waiting for the PortfolioDB tab to report a session"));
    }, timeoutMs);

    const listener = (msg: unknown): void => {
      if (typeof msg !== "object" || msg === null) return;
      if ((msg as SessionBootstrapped).type !== "session-bootstrapped") return;
      clearTimeout(timer);
      chrome.runtime.onMessage.removeListener(listener);
      resolve(msg as SessionBootstrapped);
    };
    chrome.runtime.onMessage.addListener(listener);
  });
}

function awaitTabComplete(tabId: number, timeoutMs = BOOTSTRAP_TIMEOUT_MS): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener);
      reject(new Error("timed out loading the PortfolioDB tab"));
    }, timeoutMs);

    const listener = (id: number, info: chrome.tabs.TabChangeInfo): void => {
      if (id !== tabId || info.status !== "complete") return;
      clearTimeout(timer);
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    };
    chrome.tabs.onUpdated.addListener(listener);
  });
}

export interface BootstrapResult {
  sessionId: string;
  /** Set when the page reported no session; the tab is left open to sign in. */
  needsSignIn: boolean;
  error?: string;
}

/**
 * Runs the bootstrap against the configured origin. Reuses an open PortfolioDB
 * tab when there is one; otherwise opens a background tab and closes it again,
 * unless the user needs to sign in, in which case the tab is focused and kept.
 */
export async function bootstrapSession(): Promise<BootstrapResult> {
  const { portfoliodbOrigin } = await loadConfig();
  if (!portfoliodbOrigin) {
    throw new Error("Set the PortfolioDB origin in settings first");
  }
  const pattern = `${portfoliodbOrigin}/*`;

  const granted = await chrome.permissions.request({ origins: [pattern] });
  if (!granted) {
    throw new Error(`Permission to access ${portfoliodbOrigin} was declined`);
  }

  const existing = await chrome.tabs.query({ url: pattern });
  const reusedTab = existing.find((t) => t.id !== undefined);
  let tabId = reusedTab?.id;
  let opened = false;

  if (tabId === undefined) {
    const tab = await chrome.tabs.create({ url: portfoliodbOrigin, active: false });
    if (tab.id === undefined) throw new Error("could not open a PortfolioDB tab");
    tabId = tab.id;
    opened = true;
    await awaitTabComplete(tabId);
  }

  const settled = awaitSession();
  await chrome.scripting.executeScript({ target: { tabId }, files: [CONTENT_SCRIPT] });
  const msg = await settled;

  if (!msg.sessionId) {
    // The page could not produce a session, so the user is signed out. Keep the
    // tab and bring it forward rather than closing the only way to fix it.
    await chrome.tabs.update(tabId, { active: true });
    return { sessionId: "", needsSignIn: true, error: msg.error };
  }

  if (opened) {
    await chrome.tabs.remove(tabId);
  }
  return { sessionId: msg.sessionId, needsSignIn: false };
}
