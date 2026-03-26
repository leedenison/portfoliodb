import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";

test.beforeAll(async () => {
  await loadCassette("mixed-tx-types");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("mixed transaction types", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("BUY/SELL aggregate correctly and SPLIT is silently dropped", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Upload CSV with BUY 100, BUY 50, SELL -30, SPLIT 2 (all AAPL).
    await uploadCSVAndWait(page, browser, "mixed-tx-types.csv", {
      expectedTxCount: 4,
    });

    // Navigate to holdings and verify the aggregated result.
    await page.goto("/holdings");
    await expect(
      page.locator("[data-testid='holdings-table']")
    ).toBeVisible({ timeout: 10_000 });

    // Should be exactly 1 holding row (AAPL only).
    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(1, { timeout: 10_000 });

    // Quantity = 100 + 50 - 30 = 120. SPLIT row is dropped.
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("120");
  });

  if (process.env.VCR_MODE === "record") {
    test("wait for all workers to finish (record mode)", async ({
      browser,
    }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
