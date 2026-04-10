import { test, expect } from "@playwright/test";
import { create } from "@bufbuild/protobuf";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { uploadCSVAndWait } from "../helpers/upload";
import { triggerCorporateEventFetch, importCorporateEventsAndWait } from "../helpers/api";
import { getCounter, closeCountersRedis } from "../helpers/counters";
import {
  AssetClass,
  ImportCorporateEventRowSchema,
  JobStatus,
  SplitRowSchema,
} from "../gen/api/v1/api_pb";

// ---------------------------------------------------------------------------
// Case 1: Transactions uploaded BEFORE the split is discovered.
//
// Upload stock + option txs in a single CSV (pre/post split dates). Then
// trigger the corporate event fetcher which discovers the 4:1 split from
// EODHD. Verify split-adjusted quantities and option OCC/strike update.
// ---------------------------------------------------------------------------
test.describe("stock split: tx uploaded before split", () => {
  let userSession: string;
  let adminSession: string;

  test.beforeAll(async () => {
    await loadCassette("stock-split-tx-first");
    await resetAndSeedBase();
    userSession = await seedSession("user");
    adminSession = await seedSession("admin");
  });

  test.afterAll(async () => {
    await closeCountersRedis();
    await closeRedis();
    await closeDB();
    await unloadCassette();
  });

  test("upload txs, discover split, verify adjustments", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSession);

    // Upload all txs in one CSV to avoid ReplaceTxsInPeriod conflicts
    // between overlapping date ranges on different instruments.
    await uploadCSVAndWait(page, browser, "split-txs.csv", {
      expectedTxCount: 3,
    });

    // Before the split: Adj Qty should show em-dash for all AAPL stock txs.
    await page.goto("/transactions");
    await expect(
      page.locator("[data-testid='transactions-table']"),
    ).toBeVisible();
    const preSplitRows = page.locator(
      "[data-testid='tx-row'][data-tx-instrument='AAPL']",
    );
    await expect(preSplitRows).toHaveCount(2);
    await expect(
      preSplitRows.nth(0).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("\u2014");
    await expect(
      preSplitRows.nth(1).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("\u2014");

    // Trigger the corporate event fetcher — EODHD returns a 4:1 split.
    const cyclesBefore = await getCounter("corporate_event_fetcher.cycles");
    await triggerCorporateEventFetch(adminSession);
    await expect(async () => {
      const cycles = await getCounter("corporate_event_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesBefore);
    }).toPass({ timeout: 30_000 });
    await waitForWorkersIdle(browser);

    // After the split: verify via transactions page.
    await page.goto("/transactions");
    await expect(
      page.locator("[data-testid='transactions-table']"),
    ).toBeVisible();

    // Stock tx1 (pre-split, qty=25): Adj Qty = 100.
    // Stock tx2 (post-split, qty=50): Adj Qty = em-dash (no adjustment).
    const stockRows = page.locator(
      "[data-testid='tx-row'][data-tx-instrument='AAPL']",
    );
    await expect(stockRows).toHaveCount(2);
    await expect(
      stockRows.nth(0).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("100");
    await expect(
      stockRows.nth(1).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("\u2014");

    // Option tx (pre-split, qty=1): Adj Qty = 4.
    const optRows = page
      .locator("[data-testid='tx-row']")
      .filter({ has: page.locator("[data-testid='tx-qty']", { hasText: "1" }) })
      .filter({
        has: page.locator("[data-testid='tx-adj-qty']", { hasText: "4" }),
      });
    await expect(optRows).toHaveCount(1);

    // Option OCC: verify via admin instruments page (requires admin session).
    const adminCtx = await browser.newContext();
    await injectSession(adminCtx, adminSession);
    const adminPage = await adminCtx.newPage();
    await adminPage.goto("/admin/instruments");
    const optionRow = adminPage
      .locator("[data-testid='instrument-row']")
      .filter({ hasText: /Option/i });
    await expect(optionRow).toBeVisible();
    await optionRow.click();
    const occId = adminPage.locator(
      "[data-testid='instrument-identifier'][data-identifier-type='OCC']",
    );
    await expect(occId).toContainText("AAPL250117C00190000");
    await adminCtx.close();
  });
});

// ---------------------------------------------------------------------------
// Case 2: Split imported BEFORE transactions are uploaded.
//
// Import the AAPL 4:1 split via ImportCorporateEvents (no coverage).
// Then upload stock + option txs. AdjustOCCForKnownSplits adjusts the
// option OCC during identification. Trigger the fetcher so it records
// coverage and runs processOptionSplits.
// ---------------------------------------------------------------------------
test.describe("stock split: split uploaded before tx", () => {
  let userSession: string;
  let adminSession: string;

  test.beforeAll(async () => {
    await loadCassette("stock-split-split-first");
    await resetAndSeedBase();
    userSession = await seedSession("user");
    adminSession = await seedSession("admin");
  });

  test.afterAll(async () => {
    await closeCountersRedis();
    await closeRedis();
    await closeDB();
    await unloadCassette();
  });

  test("import split, upload txs, verify adjustments", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSession);

    // Import the 4:1 split (no coverage so the fetcher will still query EODHD).
    const splitEvent = create(ImportCorporateEventRowSchema, {
      identifierType: "MIC_TICKER",
      identifierDomain: "XNAS",
      identifierValue: "AAPL",
      assetClass: AssetClass.STOCK,
      event: {
        case: "split",
        value: create(SplitRowSchema, {
          exDate: "2024-08-01",
          splitFrom: "1",
          splitTo: "4",
        }),
      },
    });
    const importResult = await importCorporateEventsAndWait(adminSession, [
      splitEvent,
    ]);
    expect(importResult.status).toBe(JobStatus.SUCCESS);

    // Upload all txs in one CSV.
    await uploadCSVAndWait(page, browser, "split-txs.csv", {
      expectedTxCount: 3,
    });

    // Trigger the fetcher to record coverage and process option splits.
    const cyclesBefore = await getCounter("corporate_event_fetcher.cycles");
    await triggerCorporateEventFetch(adminSession);
    await expect(async () => {
      const cycles = await getCounter("corporate_event_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesBefore);
    }).toPass({ timeout: 30_000 });
    await waitForWorkersIdle(browser);

    // Verify via transactions page — same final state as case 1.
    await page.goto("/transactions");
    await expect(
      page.locator("[data-testid='transactions-table']"),
    ).toBeVisible();

    const stockRows = page.locator(
      "[data-testid='tx-row'][data-tx-instrument='AAPL']",
    );
    await expect(stockRows).toHaveCount(2);
    await expect(
      stockRows.nth(0).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("100");
    await expect(
      stockRows.nth(1).locator("[data-testid='tx-adj-qty']"),
    ).toHaveText("\u2014");

    // Option Adj Qty = 4.
    const optRows = page
      .locator("[data-testid='tx-row']")
      .filter({ has: page.locator("[data-testid='tx-qty']", { hasText: "1" }) })
      .filter({
        has: page.locator("[data-testid='tx-adj-qty']", { hasText: "4" }),
      });
    await expect(optRows).toHaveCount(1);

    // Option OCC: verify via admin instruments page (requires admin session).
    const adminCtx = await browser.newContext();
    await injectSession(adminCtx, adminSession);
    const adminPage = await adminCtx.newPage();
    await adminPage.goto("/admin/instruments");
    const optionRow = adminPage
      .locator("[data-testid='instrument-row']")
      .filter({ hasText: /Option/i });
    await expect(optionRow).toBeVisible();
    await optionRow.click();
    const occId = adminPage.locator(
      "[data-testid='instrument-identifier'][data-identifier-type='OCC']",
    );
    await expect(occId).toContainText("AAPL250117C00190000");
    await adminCtx.close();
  });
});
