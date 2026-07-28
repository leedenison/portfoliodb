/** Getting hold of a loaded tab on a given site, so a script can run in its origin. */

export const TAB_TIMEOUT_MS = 15_000;

export interface HostTab {
  tabId: number;
  /** True when this call opened the tab, so the caller owns closing it. */
  opened: boolean;
}

function awaitTabComplete(tabId: number, timeoutMs = TAB_TIMEOUT_MS): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener);
      reject(new Error("timed out loading the tab"));
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

/**
 * Returns a loaded tab matching the pattern, reusing an open one when possible so
 * the user's own session and navigation are left alone. A tab opened here starts
 * in the background.
 */
export async function getHostTab(matchPattern: string, url: string): Promise<HostTab> {
  const existing = await chrome.tabs.query({ url: matchPattern });
  const reusable = existing.find((t) => t.id !== undefined);
  if (reusable?.id !== undefined) return { tabId: reusable.id, opened: false };

  const tab = await chrome.tabs.create({ url, active: false });
  if (tab.id === undefined) throw new Error(`could not open a tab at ${url}`);
  await awaitTabComplete(tab.id);
  return { tabId: tab.id, opened: true };
}
