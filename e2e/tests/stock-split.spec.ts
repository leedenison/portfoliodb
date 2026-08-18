import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { waitForWorkersIdle, getWorkerCycles, waitForWorkerCycle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { uploadArchiveAndWait } from "../helpers/upload";
import { triggerCorporateEventFetch, importCorporateEventsAndWait } from "../helpers/api";
import { JobStatus } from "../gen/api/v1/api_pb";
import { AssetClass, IdentifierType } from "../gen/type/v1/type_pb";

// ---------------------------------------------------------------------------
// Case 1: Transactions uploaded BEFORE the split is discovered.
//
// Upload stock + option txs in a single document (pre/post split dates). Then
// trigger the corporate event fetcher which discovers the 4:1 split from
// EODHD. Verify split-adjusted quantities and option OCC/strike update.
//
// split-txs.json states exported_at 2024-07-01, before the 2024-08-01 ex_date,
// and names the option by its pre-split symbol. The two go together: identity is
// stated as of the file's export, so a document exported after the ex_date would
// be claiming the pre-split symbol was current after the split restated it.
// Moving the date forward without restating the symbol makes the file say
// something false and both cases below stop testing what they are named for.
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

    // Upload all txs in one document to avoid ReplaceTxsInPeriod conflicts
    // between overlapping date ranges on different instruments.
    await uploadArchiveAndWait(page, browser, "split-txs.json", {
      expectedPostingCount: 3,
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

    // A row is one event rather than one posting: the trade's own leg is what it
    // shows, and the leg the server routed to balance it appears when the row is
    // clicked, the row being the control.
    const legs = page.locator("[data-testid='tx-leg-row']");
    await expect(legs).toHaveCount(0);
    await preSplitRows.nth(0).click();
    await expect(legs.first()).toBeVisible();

    // Trigger the corporate event fetcher — EODHD returns a 4:1 split.
    const cyclesBefore = await getWorkerCycles(browser, "corporate_event_fetcher");
    await triggerCorporateEventFetch(adminSession);
    await waitForWorkerCycle(browser, "corporate_event_fetcher", cyclesBefore);
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
    // The symbol the contract answers to now. A restated option keeps the one it
    // traded under before the ex_date too, so the locator has to say which.
    const occId = adminPage.locator(
      "[data-testid='instrument-identifier'][data-identifier-type='OCC'][data-identifier-current='true']",
    );
    await expect(occId).toContainText("AAPL250117C00190000");
    await adminCtx.close();
  });
});

// ---------------------------------------------------------------------------
// Case 2: Split imported BEFORE transactions are uploaded.
//
// Import the AAPL 4:1 split via ImportCorporateEvents (no coverage).
// Then upload stock + option txs. Knowing the split changes nothing about
// identification: the file names the contract as of its own export, which
// precedes the ex_date, so the option is stored under the pre-split symbol
// dated from that export. Trigger the fetcher so it records coverage and runs
// processOptionSplits, which is what mints the post-split name.
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
    const importResult = await importCorporateEventsAndWait(adminSession, [
      {
        instrument: {
          type: IdentifierType.MIC_TICKER,
          value: "AAPL",
          domain: "XNAS",
        },
        assetClass: AssetClass.STOCK,
        events: [
          {
            event: {
              case: "split",
              value: { exDate: "2024-08-01", splitFrom: "1", splitTo: "4" },
            },
          },
        ],
      },
    ]);
    expect(importResult.status).toBe(JobStatus.SUCCESS);

    // Upload all txs in one document.
    await uploadArchiveAndWait(page, browser, "split-txs.json", {
      expectedPostingCount: 3,
    });

    // Trigger the fetcher to record coverage and process option splits.
    const cyclesBefore = await getWorkerCycles(browser, "corporate_event_fetcher");
    await triggerCorporateEventFetch(adminSession);
    await waitForWorkerCycle(browser, "corporate_event_fetcher", cyclesBefore);
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
    // The symbol the contract answers to now. A restated option keeps the one it
    // traded under before the ex_date too, so the locator has to say which.
    const occId = adminPage.locator(
      "[data-testid='instrument-identifier'][data-identifier-type='OCC'][data-identifier-current='true']",
    );
    await expect(occId).toContainText("AAPL250117C00190000");
    await adminCtx.close();
  });
});
