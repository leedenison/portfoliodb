import { test, expect } from "@playwright/test";
import path from "path";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import {
  resetAndSeedBase,
  corruptMassivePriceKey,
  getClient,
  closeDB,
} from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";

test.beforeAll(async () => {
  await loadCassette("fetch-blocks");
  await resetAndSeedBase();
  if (process.env.VCR_MODE === "record") {
    await corruptMassivePriceKey();
  }
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("fetch block full flow", () => {
  let userSessionId: string;
  let adminSessionId: string;

  test.beforeAll(async () => {
    userSessionId = await seedSession("user");
    adminSessionId = await seedSession("admin");
  });

  test("ingestion with bad price key creates fetch blocks", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSessionId);

    // Upload CSV to trigger ingestion -> identification -> price fetch (403).
    await page.goto("/uploads");
    await expect(
      page.locator("[data-testid='page-uploads']")
    ).toBeVisible();

    await page.locator("[data-testid='btn-upload-transactions']").click();
    await expect(
      page.locator("[data-testid='upload-modal']")
    ).toBeVisible();

    // Step 1: broker pre-selected, click Next.
    await page.getByRole("button", { name: "Next" }).click();

    // Step 2: set the CSV file.
    const fileInput = page.locator("#upload-file");
    await fileInput.setInputFiles(
      path.resolve(__dirname, "../fixtures/fetch-blocks-stocks.csv")
    );

    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toBeVisible();
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toContainText("3 transaction(s)");

    // Upload and wait for ingestion to complete.
    await page.locator("[data-testid='btn-upload-submit']").click();
    await expect(
      page.locator("[data-testid='upload-modal']")
    ).not.toBeVisible({ timeout: 30_000 });

    // Wait for all workers (ingestion + identification + price fetcher).
    await waitForWorkersIdle(browser);

    // --- DIAGNOSTIC QUERIES (temporary) ---
    const pg = await getClient();
    const instruments = await pg.query(`
      SELECT i.id, i.asset_class, i.currency, i.name
      FROM instruments i
      WHERE i.asset_class NOT IN ('CASH', 'FX') OR i.asset_class IS NULL
    `);
    console.log("DIAG instruments:", JSON.stringify(instruments.rows, null, 2));

    const identifiers = await pg.query(`
      SELECT ii.instrument_id, ii.identifier_type, ii.value
      FROM instrument_identifiers ii
      JOIN instruments i ON i.id = ii.instrument_id
      WHERE i.asset_class NOT IN ('CASH', 'FX') OR i.asset_class IS NULL
    `);
    console.log("DIAG identifiers:", JSON.stringify(identifiers.rows, null, 2));

    const idErrors = await pg.query(`
      SELECT job_id, instrument_description, message
      FROM identification_errors
    `);
    console.log("DIAG identification_errors:", JSON.stringify(idErrors.rows, null, 2));

    const blocks = await pg.query(`SELECT * FROM price_fetch_blocks`);
    console.log("DIAG fetch_blocks:", JSON.stringify(blocks.rows, null, 2));

    const plugins = await pg.query(`
      SELECT plugin_id, category, enabled, precedence
      FROM plugin_config ORDER BY category, precedence
    `);
    console.log("DIAG plugin_config:", JSON.stringify(plugins.rows, null, 2));
    // --- END DIAGNOSTIC QUERIES ---

    // Switch to admin context and check fetch blocks.
    const adminContext = await browser.newContext();
    await injectSession(adminContext, adminSessionId);
    const adminPage = await adminContext.newPage();

    await adminPage.goto("/admin/prices");
    await adminPage.getByRole("button", { name: "Price Fetch Blocks" }).click();

    const table = adminPage.locator("[data-testid='fetch-blocks-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = adminPage.locator("[data-testid='fetch-block-row']");
    await expect(rows).toHaveCount(3, { timeout: 10_000 });

    // Verify all three instruments are blocked with "forbidden" reason.
    await expect(table).toContainText("Amazon");
    await expect(table).toContainText("NVIDIA");
    await expect(table).toContainText("Tesla");
    await expect(table).toContainText("forbidden");

    await adminContext.close();
  });

  test("admin can clear a fetch block", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/prices");
    await page.getByRole("button", { name: "Price Fetch Blocks" }).click();

    const table = page.locator("[data-testid='fetch-blocks-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='fetch-block-row']");
    await expect(rows).toHaveCount(3, { timeout: 10_000 });

    // Clear the first block.
    const firstClearBtn = page
      .locator("[data-testid='fetch-block-clear-btn']")
      .first();
    await firstClearBtn.click();

    // Row count should decrease to 2.
    await expect(rows).toHaveCount(2, { timeout: 10_000 });
  });

  test("clearing all blocks shows empty state", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/prices");
    await page.getByRole("button", { name: "Price Fetch Blocks" }).click();

    const rows = page.locator("[data-testid='fetch-block-row']");
    await expect(rows).toHaveCount(2, { timeout: 10_000 });

    // Clear remaining blocks one at a time.
    await page
      .locator("[data-testid='fetch-block-clear-btn']")
      .first()
      .click();
    await expect(rows).toHaveCount(1, { timeout: 10_000 });

    await page
      .locator("[data-testid='fetch-block-clear-btn']")
      .first()
      .click();

    // Empty state should appear.
    await expect(
      page.locator("[data-testid='fetch-blocks-empty']")
    ).toBeVisible({ timeout: 10_000 });
    await expect(
      page.locator("[data-testid='fetch-blocks-empty']")
    ).toContainText("No blocked instruments.");
  });

  // In record mode, ensure all workers finish so the VCR cassette captures
  // every HTTP interaction before the cassette is unloaded.
  if (process.env.VCR_MODE === "record") {
    test("wait for all workers to finish (record mode)", async ({
      browser,
    }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
