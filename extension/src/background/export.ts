/**
 * Runs a recipe's export request in the broker's own page.
 *
 * The request has to originate from the broker's origin so its session cookie is
 * attached, which a service worker request would not be. Unlike the session
 * bootstrap this needs no bundled content script: the whole job is one fetch, so
 * the function is injected directly with the rendered request as its argument.
 */

import { renderExport } from "../brokers";
import type { BrokerRecipe, DateWindow } from "../brokers/types";
import { hasPatternPermission } from "../lib/permissions";
import { getHostTab } from "./tabs";

export interface CapturedExport {
  status: number;
  body: string;
}

/** Serialized into the page, so it may not close over anything. */
function fetchInPage(req: {
  url: string;
  method: string;
  headers?: Record<string, string>;
  body?: string;
}): Promise<{ status: number; body: string }> {
  return fetch(req.url, {
    method: req.method,
    credentials: "include",
    ...(req.headers ? { headers: req.headers } : {}),
    ...(req.body ? { body: req.body } : {}),
  }).then((r) => r.text().then((body) => ({ status: r.status, body })));
}

export async function captureExport(
  recipe: BrokerRecipe,
  window: DateWindow
): Promise<CapturedExport> {
  const pattern = recipe.origins[0];
  if (!pattern) throw new Error(`recipe ${recipe.id} declares no origins`);
  // Granting happens in the popup, where the user gesture is.
  if (!(await hasPatternPermission(recipe.origins))) {
    throw new Error(`Permission to access ${recipe.origins.join(", ")} has not been granted`);
  }

  const req = renderExport(recipe, window);
  const tab = await getHostTab(pattern, recipe.homeUrl);
  try {
    const [injection] = await chrome.scripting.executeScript({
      target: { tabId: tab.tabId },
      func: fetchInPage,
      args: [req],
    });
    const result = injection?.result as CapturedExport | undefined;
    if (!result) throw new Error("the export request returned nothing");
    if (result.status !== 200) {
      // A signed-out session answers with a redirect to the login page rather
      // than an error, so the body is HTML instead of the expected payload.
      throw new Error(
        `${recipe.id} returned HTTP ${result.status}; sign in to the broker and try again`
      );
    }
    return result;
  } finally {
    if (tab.opened) await chrome.tabs.remove(tab.tabId);
  }
}
