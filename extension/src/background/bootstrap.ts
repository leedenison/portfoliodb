/**
 * Drives the session bootstrap: get permission for the configured origin, get a
 * tab on it, inject the bootstrap content script, and wait for the session id it
 * sends back.
 */

import { loadConfig } from "../config";
import type { SessionBootstrapped } from "../lib/messages";
import { hasOriginPermission, originPattern } from "../lib/permissions";
import { getHostTab, TAB_TIMEOUT_MS as BOOTSTRAP_TIMEOUT_MS } from "./tabs";

const CONTENT_SCRIPT = "content/portfoliodb.js";

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
  const pattern = originPattern(portfoliodbOrigin);

  // Granting happens in the popup, which has the user gesture that
  // permissions.request requires; the worker only checks.
  if (!(await hasOriginPermission(portfoliodbOrigin))) {
    throw new Error(`Permission to access ${portfoliodbOrigin} has not been granted`);
  }

  const { tabId, opened } = await getHostTab(pattern, portfoliodbOrigin);

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
