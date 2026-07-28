/**
 * Service worker entry point.
 *
 * Owns the session: runs the bootstrap on request, stores the id, and answers
 * status queries from the popup. Sync orchestration arrives in a later change.
 */

import { bootstrapSession } from "./bootstrap";
import { dryRun } from "./dry-run";
import { sync } from "./sync";
import { getRecipe } from "../brokers";
import { listRuns } from "../lib/run-log";
import { loadConfig } from "../config";
import { getSession } from "../lib/api";
import type { Message, SessionStatus } from "../lib/messages";
import { clearSessionId, getSessionId, setSessionId } from "../lib/session";
import { registerSessionLostHandler } from "@/lib/session-lost";

// The shared transport calls this whenever a response comes back UNAUTHENTICATED,
// so an expired session is dropped at the point it is detected rather than being
// retried with a dead id.
registerSessionLostHandler(() => {
  void clearSessionId();
});

/** Confirms the stored session works from the service worker's own bearer path. */
async function status(): Promise<SessionStatus> {
  const sessionId = await getSessionId();
  if (!sessionId) return { connected: false };
  const { portfoliodbOrigin } = await loadConfig();
  if (!portfoliodbOrigin) return { connected: false, error: "No PortfolioDB origin configured" };
  try {
    const res = await getSession(portfoliodbOrigin, sessionId);
    return { connected: true, email: res.user?.email };
  } catch (e) {
    await clearSessionId();
    return { connected: false, error: e instanceof Error ? e.message : String(e) };
  }
}

async function connect(): Promise<SessionStatus> {
  try {
    const result = await bootstrapSession();
    if (!result.sessionId) {
      return {
        connected: false,
        error: result.needsSignIn
          ? "Sign in to PortfolioDB in the tab that just opened, then connect again"
          : (result.error ?? "Bootstrap failed"),
      };
    }
    await setSessionId(result.sessionId);
    return await status();
  } catch (e) {
    return { connected: false, error: e instanceof Error ? e.message : String(e) };
  }
}

chrome.runtime.onMessage.addListener((msg: Message, _sender, sendResponse) => {
  if (msg?.type === "status") {
    void status().then(sendResponse);
    return true; // response is async
  }
  if (msg?.type === "connect") {
    void connect().then(sendResponse);
    return true;
  }
  if (msg?.type === "dry-run") {
    void dryRun(msg).then(sendResponse);
    return true;
  }
  if (msg?.type === "sync") {
    const recipe = getRecipe(msg.recipeId);
    if (!recipe) {
      sendResponse({ entry: { startedAt: new Date().toISOString(), recipeId: msg.recipeId, status: "failed", error: `no recipe named ${msg.recipeId}` } });
      return false;
    }
    void sync({ broker: recipe.broker }).then(sendResponse);
    return true;
  }
  if (msg?.type === "runs") {
    void listRuns().then(sendResponse);
    return true;
  }
  return false;
});
