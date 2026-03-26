import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { waitForWorkersIdle } from "../helpers/workers";
import { uploadCSVAndWait } from "../helpers/upload";

test.beforeAll(async () => {
  await loadCassette("failed-identification");
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("failed instrument identification", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("unresolvable instrument shows as broker-description-only in holdings", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Upload CSV with one resolvable (AAPL) and one unresolvable (XYZFAKE).
    await uploadCSVAndWait(page, browser, "mixed-identification.csv", {
      expectedTxCount: 2,
    });

    // Both instruments should appear in holdings.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='holdings-table'] tbody tr");
    await expect(rows).toHaveCount(2, { timeout: 10_000 });

    // AAPL should be resolved (shows ticker).
    await expect(table).toContainText("AAPL");

    // XYZFAKE should fall back to instrument description (no ticker resolved).
    await expect(table).toContainText("XYZFAKE");
  });

  test("identification errors are visible on the uploads page", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);

    // Navigate to uploads page and wait for job rows to load.
    await page.goto("/uploads");
    await expect(
      page.locator("[data-testid='page-uploads']")
    ).toBeVisible();

    // Wait for the table body to have at least one row (job list loaded).
    const firstJobRow = page.locator(
      "[data-testid='page-uploads'] table tbody tr"
    ).first();
    await expect(firstJobRow).toBeVisible({ timeout: 10_000 });

    // The job should show a non-zero error count in the last column.
    const errorCell = firstJobRow.locator("td").last();
    await expect(errorCell).not.toHaveText("0");

    // Click the first job row to expand and load error details.
    await firstJobRow.click();

    // The expanded detail should show identification errors for XYZFAKE.
    // The description plugin extracts "XYZFAKE" as a ticker, but all
    // identifier plugins fail to resolve it → "broker description only".
    const heading = page.getByRole("heading", { name: /identification errors/i });
    await expect(heading).toBeVisible({ timeout: 10_000 });
    await expect(page.getByText("XYZFAKE")).toBeVisible();
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
