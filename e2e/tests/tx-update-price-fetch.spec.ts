import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";
import { getCounter, closeCountersRedis } from "../helpers/counters";

test.beforeAll(async () => {
  await loadCassette("tx-update-price-fetch");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeCountersRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("no redundant price fetch after transaction update", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("adding a transaction within an already-covered holding period triggers no new price API calls", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Upload initial transaction: buy 10 INTC ~6 months ago.
    // This triggers identification and price fetch for the held period.
    await uploadCSVAndWait(page, browser, "tx-update-initial.csv", {
      expectedTxCount: 1,
    });

    // Verify the holding appears.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });
    await expect(table).toContainText("INTC");

    // Record counters after the initial price fetch completes.
    const cyclesAfterFirst = await getCounter("price_fetcher.cycles");
    const massiveAfterFirst = await getCounter(
      "prices.fetch.massive.request.succeeded"
    );
    expect(massiveAfterFirst).toBeGreaterThan(0);

    // Upload additional transaction: buy 5 more INTC ~3 months ago.
    // The holding period is unchanged (still ~6 months ago to today),
    // just with a larger position from ~3 months ago onwards.
    await uploadCSVAndWait(page, browser, "tx-update-additional.csv", {
      expectedTxCount: 1,
    });

    // Wait for the price fetcher cycle triggered by the new transaction.
    // The cycle finds no gaps (prices already cover the held period) so
    // the worker never transitions to RUNNING. Poll the cycle counter to
    // confirm the cycle actually executed.
    await expect(async () => {
      const cycles = await getCounter("price_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesAfterFirst);
    }).toPass({ timeout: 30_000 });

    // Verify no new Massive price API calls were made.
    const massiveAfter = await getCounter(
      "prices.fetch.massive.request.succeeded"
    );
    expect(massiveAfter).toBe(massiveAfterFirst);
  });

  // In record mode, ensure all workers finish so the VCR cassette captures
  // every HTTP interaction before the server shuts down.
  if (process.env.VCR_MODE === "record") {
    test("wait for all workers to finish (record mode)", async ({
      browser,
    }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
