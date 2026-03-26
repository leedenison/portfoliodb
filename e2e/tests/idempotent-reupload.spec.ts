import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";

test.beforeAll(async () => {
  await loadCassette("idempotent-reupload");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("idempotent bulk re-upload", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("re-uploading same CSV does not double-count holdings", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // First upload: AAPL 10, MSFT 5, GOOGL 20.
    await uploadCSVAndWait(page, browser, "standard-3-stocks.csv", {
      expectedTxCount: 3,
    });

    // Verify initial holdings.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(3, { timeout: 10_000 });
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("10");
    await expect(table).toContainText("MSFT");
    await expect(table).toContainText("GOOGL");
    await expect(table).toContainText("20");

    // Second upload: exact same CSV. Should replace, not append.
    await uploadCSVAndWait(page, browser, "standard-3-stocks.csv", {
      expectedTxCount: 3,
    });

    // Verify holdings are identical (not doubled).
    await page.goto("/holdings");
    await expect(table).toBeVisible({ timeout: 10_000 });
    await expect(rows).toHaveCount(3, { timeout: 10_000 });
    await expect(table).toContainText("10");
    await expect(table).not.toContainText("20\n10"); // no double entries
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("MSFT");
    await expect(table).toContainText("GOOGL");
  });

  test("modified re-upload replaces holdings for same period", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Third upload: AAPL 15, MSFT 5, AMZN 8 (GOOGL removed).
    await uploadCSVAndWait(page, browser, "reupload-modified.csv", {
      expectedTxCount: 3,
    });

    // Verify holdings reflect the replacement.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(3, { timeout: 10_000 });

    // AAPL quantity changed from 10 to 15.
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("15");

    // MSFT unchanged.
    await expect(table).toContainText("MSFT");

    // AMZN added, GOOGL gone.
    await expect(table).toContainText("AMZN");
    await expect(table).not.toContainText("GOOGL");
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
