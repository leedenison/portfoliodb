import { test, expect } from "@playwright/test";
import { seedSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, seedFixture } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { getCounter, closeCountersRedis } from "../helpers/counters";
import { setDisplayCurrency } from "../helpers/api";
import { TIMEOUT_REGULAR } from "../helpers/timeouts";

test.beforeAll(async () => {
  await resetAndSeedBase();
  await seedFixture("trigger-debounce.sql");
});

test.afterAll(async () => {
  await closeRedis();
  await closeCountersRedis();
  await closeDB();
});

test.describe("price trigger debounce", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("three rapid currency changes produce at most two price fetch cycles", async ({
    browser,
  }) => {
    test.setTimeout(TIMEOUT_REGULAR);

    // Wait for any startup activity to settle.
    await waitForWorkersIdle(browser);

    const before = await getCounter("price_fetcher.cycles");

    // Fire three SetDisplayCurrency calls concurrently.
    // The trigger channel (buffer size 1) should collapse these into at most
    // two cycles: one running immediately, one queued, third dropped.
    await Promise.all([
      setDisplayCurrency(sessionId, "GBP"),
      setDisplayCurrency(sessionId, "EUR"),
      setDisplayCurrency(sessionId, "JPY"),
    ]);

    // Wait for the price fetcher to finish all cycles.
    await waitForWorkersIdle(browser);

    const after = await getCounter("price_fetcher.cycles");
    const cycles = after - before;

    // With a buffer-1 trigger channel: the first trigger starts a cycle,
    // the second is buffered, the third is dropped. At most 2 cycles.
    expect(cycles).toBeGreaterThanOrEqual(1);
    expect(cycles).toBeLessThanOrEqual(2);
  });
});
