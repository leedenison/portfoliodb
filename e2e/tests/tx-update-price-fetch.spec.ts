import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";
import { getCounter, closeCountersRedis } from "../helpers/counters";
import { Client } from "pg";

const DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  "postgres://portfoliodb:portfoliodb@localhost:5434/portfoliodb";

let diagDB: Client;

test.beforeAll(async ({ browser }) => {
  await loadCassette("tx-update-price-fetch");
  await waitForWorkersIdle(browser);
  await resetAndSeedBase();
  diagDB = new Client(DATABASE_URL);
  await diagDB.connect();
});

test.afterAll(async () => {
  await diagDB.end();
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

    // DIAG checkpoint 1: DB should be clean after reset.
    const txsBefore = await diagDB.query(
      `SELECT instrument_id, timestamp, instrument_description FROM txs`
    );
    const instsBefore = await diagDB.query(
      `SELECT id, asset_class, name FROM instruments
       WHERE asset_class IS NULL OR asset_class NOT IN ('CASH', 'FX')`
    );
    console.log("DIAG checkpoint 1 - txs:", JSON.stringify(txsBefore.rows));
    console.log(
      "DIAG checkpoint 1 - instruments:",
      JSON.stringify(instsBefore.rows)
    );

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

    // DIAG checkpoint 2: after first upload + workers idle.
    const txsAfter = await diagDB.query(
      `SELECT instrument_id, timestamp, instrument_description
       FROM txs ORDER BY timestamp`
    );
    const instsAfter = await diagDB.query(
      `SELECT id, asset_class, name FROM instruments
       WHERE asset_class IS NULL OR asset_class NOT IN ('CASH', 'FX')`
    );
    console.log("DIAG checkpoint 2 - txs:", JSON.stringify(txsAfter.rows));
    console.log(
      "DIAG checkpoint 2 - instruments:",
      JSON.stringify(instsAfter.rows)
    );

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
