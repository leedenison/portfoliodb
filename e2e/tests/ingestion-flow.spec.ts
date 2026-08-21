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
  await resetAndSeedBase("ingestion-flow");
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("archive ingestion flow", () => {
  // The exported_at field below shows the local calendar day of the envelope's
  // instant, so the day it reads depends on the browser's zone. The document
  // states UTC instants and this path takes its window from the document rather
  // than from local midnights, so pinning UTC fixes the assertion without
  // changing what the upload does.
  test.use({ timezoneId: "UTC" });

  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("upload an archive document, wait for job completion, verify holdings", async ({
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

    // Step 2: format defaults to the archive document. Set the file on the hidden
    // input (Playwright can set files on hidden inputs directly).
    const fileInput = page.locator("#upload-file");
    await fileInput.setInputFiles(
      path.resolve(__dirname, "../fixtures/standard-3-stocks.json")
    );

    // Wait for the parse preview to appear.
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toBeVisible();
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toContainText("3 posting(s)");

    // The vintage the upload will state. A document dates itself, so the field
    // shows the envelope's exported_at rather than today: the identifiers in the
    // file are the ones its export was written against.
    await expect(page.locator("[data-testid='upload-exported-at']")).toHaveValue(
      "2026-08-01"
    );

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

// A broker file that dates nothing still has to state a vintage, because the
// identifiers in it are as of its export and that is what dates the names a
// resolution writes from them.
// The upload offers the last day the file covers -- the earliest date an export
// could honestly claim -- and lets it be corrected. Nothing is uploaded here: the
// question is what the modal proposes.
test.describe("upload vintage for a file that dates nothing", () => {
  // The field shows a local calendar day and the converter builds the window from
  // local midnights, so the two agree in any single zone; pinning one keeps the
  // expected day written down rather than computed.
  test.use({ timezoneId: "UTC" });

  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("offers the last day the file covers", async ({ context, page }) => {
    await injectSession(context, sessionId);

    await page.goto("/uploads");
    await page.locator("[data-testid='btn-upload-transactions']").click();
    await expect(page.locator("[data-testid='upload-modal']")).toBeVisible();
    await page.getByRole("button", { name: "Next" }).click();

    // A Fidelity CSV: no envelope, and the download states no export date.
    await page.locator("#upload-format").selectOption("fidelity-csv");
    await page.locator("#fidelity-currency").selectOption("GBP");
    await page
      .locator("#upload-file")
      .setInputFiles(path.resolve(__dirname, "../fixtures/fidelity-two-trades.csv"));

    await expect(page.locator("[data-testid='upload-parse-preview']")).toBeVisible();
    // The window runs to the day after the last order date, so 21 Jan 2026 is the
    // last day it covers and the earliest date this file could claim.
    await expect(page.locator("[data-testid='upload-exported-at']")).toHaveValue("2026-01-21");

    // And it is the user's to correct, for a file that has been sitting on disk.
    await page.locator("[data-testid='upload-exported-at']").fill("2026-02-10");
    await expect(page.locator("[data-testid='upload-exported-at']")).toHaveValue("2026-02-10");
  });
});

test.describe("upload validation errors", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("a file the format rejects shows an error and disables upload", async ({
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

    // Step 2: upload the bad archive document.
    const fileInput = page.locator("#upload-file");
    await fileInput.setInputFiles(
      path.resolve(__dirname, "../fixtures/bad-format.json")
    );

    // The error list should appear with parse errors.
    await expect(
      page.locator("[data-testid='upload-parse-errors']")
    ).toBeVisible();

    // Whether a file is a valid archive is an all-or-nothing question answered
    // at parse time, so one error is what the format has to say about it. Which
    // rows fail against this instance is a separate question the job answers.
    const errorItems = page.locator("[data-testid='upload-parse-errors'] li");
    await expect(errorItems).toHaveCount(1);

    // The upload button should NOT be visible (errors prevent upload).
    await expect(
      page.locator("[data-testid='btn-upload-submit']")
    ).not.toBeVisible();
  });
});
