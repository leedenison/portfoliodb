import { test, expect } from "@playwright/test";
import path from "path";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { isRecordingSuite } from "../helpers/vcr";

test.beforeAll(async () => {
  await loadCassette("ingestion-flow");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("CSV ingestion flow", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("upload CSV, wait for job completion, verify holdings", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Navigate to uploads page.
    await page.goto("/uploads");
    await expect(
      page.locator("[data-testid='page-uploads']")
    ).toBeVisible();

    // Open the upload modal.
    await page.locator("[data-testid='btn-upload-transactions']").click();
    await expect(
      page.locator("[data-testid='upload-modal']")
    ).toBeVisible();

    // Step 1: broker is pre-selected (Fidelity, the only option). Click Next.
    await page.getByRole("button", { name: "Next" }).click();

    // Step 2: format defaults to "Standard". Set the CSV file on the hidden
    // input (Playwright can set files on hidden inputs directly).
    const fileInput = page.locator("#upload-file");
    await fileInput.setInputFiles(
      path.resolve(__dirname, "../fixtures/standard-3-stocks.csv")
    );

    // Wait for the parse preview to appear.
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toBeVisible();
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toContainText("3 transaction(s)");

    // Click Upload.
    await page.locator("[data-testid='btn-upload-submit']").click();

    // The modal shows a spinner while the worker processes. It polls getJob()
    // every 2s and auto-closes on SUCCESS.
    await expect(
      page.locator("[data-testid='upload-modal']")
    ).not.toBeVisible({ timeout: 30_000 });

    // Wait for all background workers (ingestion + price fetcher) to finish.
    await waitForWorkersIdle(browser);

    // Navigate to holdings and verify the 3 instruments appear.
    await page.goto("/holdings");
    await expect(
      page.locator("[data-testid='holdings-table']")
    ).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(3, { timeout: 10_000 });

    // Verify instrument descriptions are present in the table.
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("MSFT");
    await expect(table).toContainText("GOOGL");
  });

  // In record mode, ensure all price fetches complete so the VCR cassette
  // captures every HTTP interaction before the server shuts down.
  if (isRecordingSuite("ingestion-flow")) {
    test("wait for all workers to finish (record mode)", async ({ browser }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});

test.describe("upload validation errors", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("malformed CSV shows parse errors and disables upload", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);

    await page.goto("/uploads");
    await page.locator("[data-testid='btn-upload-transactions']").click();
    await expect(
      page.locator("[data-testid='upload-modal']")
    ).toBeVisible();

    // Step 1: click Next.
    await page.getByRole("button", { name: "Next" }).click();

    // Step 2: upload the bad CSV.
    const fileInput = page.locator("#upload-file");
    await fileInput.setInputFiles(
      path.resolve(__dirname, "../fixtures/bad-format.csv")
    );

    // The error list should appear with parse errors.
    await expect(
      page.locator("[data-testid='upload-parse-errors']")
    ).toBeVisible();

    // There should be multiple error entries (invalid date, empty description,
    // unknown type).
    const errorItems = page.locator("[data-testid='upload-parse-errors'] li");
    await expect(errorItems).toHaveCount(3);

    // The upload button should NOT be visible (errors prevent upload).
    await expect(
      page.locator("[data-testid='btn-upload-submit']")
    ).not.toBeVisible();
  });
});
