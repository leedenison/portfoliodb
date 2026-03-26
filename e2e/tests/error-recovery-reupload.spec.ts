import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";

test.beforeAll(async () => {
  await loadCassette("error-recovery-reupload");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("error recovery via corrected re-upload", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("upload with identification errors, then fix via corrected re-upload", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // First upload: AAPL (resolvable) + XYZFAKE (unresolvable).
    await uploadCSVAndWait(page, browser, "mixed-identification.csv", {
      expectedTxCount: 2,
    });

    // Verify holdings show both (AAPL resolved, XYZFAKE as description).
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(2, { timeout: 10_000 });
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("XYZFAKE");

    // Verify identification errors on uploads page.
    await page.goto("/uploads");
    await expect(
      page.locator("[data-testid='page-uploads']")
    ).toBeVisible();
    const firstRow = page.locator(
      "[data-testid='page-uploads'] table tbody tr"
    ).first();
    await expect(firstRow).toBeVisible({ timeout: 10_000 });
    await firstRow.click();
    await expect(
      page.getByRole("heading", { name: /identification errors/i })
    ).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("XYZFAKE")).toBeVisible();

    // Corrected re-upload: AAPL + MSFT (with ISIN) for same period.
    // This replaces the previous upload via idempotent bulk semantics.
    await uploadCSVAndWait(page, browser, "identification-corrected.csv", {
      expectedTxCount: 2,
    });

    // Verify holdings now show AAPL + MSFT (both resolved). XYZFAKE gone.
    await page.goto("/holdings");
    await expect(table).toBeVisible({ timeout: 10_000 });
    await expect(rows).toHaveCount(2, { timeout: 10_000 });
    await expect(table).toContainText("AAPL");
    await expect(table).toContainText("MSFT");
    await expect(table).not.toContainText("XYZFAKE");

    // Verify the latest upload has no errors.
    await page.goto("/uploads");
    // The most recent job (first row) should have 0 errors.
    const errorCells = page.locator("table tbody tr:first-child td:last-child");
    await expect(errorCells).toContainText("0");
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
