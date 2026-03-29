import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, seedFixture } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";
import { getCounter, closeCountersRedis } from "../helpers/counters";

test.beforeAll(async () => {
  await loadCassette("tx-update-price-fetch");
  await resetAndSeedBase();
  await seedFixture("tx-update-price-fetch.sql");
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

    // The SQL fixture pre-seeds INTC with identifiers, one transaction
    // (buy 10 on 2024-01-15), and dense synthetic price coverage from
    // 2024-01-10 through today. This guarantees PriceGaps returns empty
    // without relying on a real API fetch (which may leave persistent
    // gaps if the API lacks early historical data).

    // Wait for any startup activity to settle.
    await waitForWorkersIdle(browser);

    // Verify the pre-seeded holding appears.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });
    await expect(table).toContainText("INTC");

    // Record counters before the second upload.
    const cyclesBefore = await getCounter("price_fetcher.cycles");
    const massiveBefore = await getCounter(
      "prices.fetch.massive.request.succeeded"
    );

    // Upload additional transaction: buy 5 more INTC on 2024-02-15.
    // The holding period is unchanged (still 2024-01-15 to today), just
    // with a larger position from 2024-02-15 onwards. Prices already
    // cover the entire held range so no new fetch is needed.
    await uploadCSVAndWait(page, browser, "tx-update-additional.csv", {
      expectedTxCount: 1,
    });

    // Wait for the price fetcher cycle triggered by the new transaction.
    // The cycle finds no gaps (prices already cover the held period) so
    // the worker never transitions to RUNNING. Poll the cycle counter to
    // confirm the cycle actually executed.
    await expect(async () => {
      const cycles = await getCounter("price_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesBefore);
    }).toPass({ timeout: 30_000 });

    // Verify no new Massive price API calls were made.
    const massiveAfter = await getCounter(
      "prices.fetch.massive.request.succeeded"
    );
    expect(massiveAfter).toBe(massiveBefore);
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
