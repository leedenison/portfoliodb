// Reads background worker status from the admin Workers page.
// Uses a Playwright browser to render the page and read state and the
// completed-cycle count from data attributes.

import type { Browser, BrowserContext, Page } from "@playwright/test";
import { seedSession, injectSession } from "./auth";

// Open the admin workers page in its own context. The caller owns the context
// and must close it. The page refreshes itself every 2 seconds, so a caller can
// poll the data attributes without reloading.
async function openWorkersPage(
  browser: Browser
): Promise<{ context: BrowserContext; page: Page }> {
  const adminSession = await seedSession("admin");
  const context = await browser.newContext();
  await injectSession(context, adminSession);
  const page = await context.newPage();
  await page.goto("/admin/workers");
  return { context, page };
}

// Read the number of cycles the named worker has completed since the service
// started. Returns 0 for a worker that has not registered yet.
//
// This is the only way to observe a cycle that found no work: such a cycle never
// reaches the running state, so waitForWorkersIdle cannot tell it from a cycle
// that has not begun.
export async function getWorkerCycles(
  browser: Browser,
  name: string
): Promise<number> {
  const { context, page } = await openWorkersPage(browser);
  try {
    return await readCycles(page, name);
  } finally {
    await context.close();
  }
}

// Wait until the named worker has completed more cycles than `after`.
export async function waitForWorkerCycle(
  browser: Browser,
  name: string,
  after: number,
  opts?: {
    pollMs?: number;
    timeoutMs?: number;
  }
): Promise<void> {
  const pollMs = opts?.pollMs ?? 500;
  const timeoutMs = opts?.timeoutMs ?? 30_000;

  const { context, page } = await openWorkersPage(browser);
  try {
    const deadline = Date.now() + timeoutMs;
    let cycles = 0;
    while (Date.now() < deadline) {
      cycles = await readCycles(page, name);
      if (cycles > after) return;
      await new Promise((r) => setTimeout(r, pollMs));
    }
    throw new Error(
      `waitForWorkerCycle timed out after ${timeoutMs}ms: ${name} is at ${cycles} cycles, waiting for more than ${after}`
    );
  } finally {
    await context.close();
  }
}

// Read data-worker-cycles for one worker off an open workers page.
async function readCycles(page: Page, name: string): Promise<number> {
  const cell = page.locator(`[data-worker-name="${name}"]`);
  if ((await cell.count()) === 0) return 0;
  const value = await cell.first().getAttribute("data-worker-cycles");
  return value ? parseInt(value, 10) : 0;
}

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

  const { context, page } = await openWorkersPage(browser);

  try {
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
