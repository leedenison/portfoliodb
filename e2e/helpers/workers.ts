// Polls the admin Workers page until all workers are idle.
// Uses a Playwright browser to render the page and read worker state
// from data attributes.

import type { Browser, BrowserContext } from "@playwright/test";
import { seedSession, injectSession } from "./auth";

// Wait for all background workers to reach idle state with empty queues.
// Opens a new browser context, navigates to the admin workers page, and
// polls until every worker-row shows data-worker-state="idle".
export async function waitForWorkersIdle(
  browser: Browser,
  opts?: {
    pollMs?: number;
    timeoutMs?: number;
  }
): Promise<void> {
  const pollMs = opts?.pollMs ?? 500;
  const timeoutMs = opts?.timeoutMs ?? 120_000;

  const adminSession = await seedSession("admin");
  const context = await browser.newContext();
  await injectSession(context, adminSession);
  const page = await context.newPage();

  try {
    await page.goto("/admin/workers");

    const deadline = Date.now() + timeoutMs;

    while (Date.now() < deadline) {
      // Wait for at least one worker row to appear.
      const rows = page.locator("[data-worker-state]");
      const count = await rows.count();

      if (count > 0) {
        const states = await rows.evaluateAll((els) =>
          els.map((el) => el.getAttribute("data-worker-state"))
        );
        if (states.every((s) => s === "idle")) return;
      }

      await new Promise((r) => setTimeout(r, pollMs));
    }

    // Timeout — gather diagnostics.
    const rows = page.locator("[data-worker-state]");
    const diag = await rows.evaluateAll((els) =>
      els.map((el) => `${el.getAttribute("data-worker-name")}=${el.getAttribute("data-worker-state")}`)
    );
    throw new Error(
      `waitForWorkersIdle timed out after ${timeoutMs}ms. Workers: ${diag.join(", ")}`
    );
  } finally {
    await context.close();
  }
}
