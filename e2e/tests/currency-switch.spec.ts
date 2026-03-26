import { test, expect } from "@playwright/test";
import { TIMEOUT_REGULAR } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, seedFixture, closeDB } from "../helpers/db";

test.beforeAll(async () => {
  await resetAndSeedBase();
  await seedFixture("instruments.sql");
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("display currency switch", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("changing display currency updates performance chart symbol", async ({
    context,
    page,
  }) => {
    test.setTimeout(TIMEOUT_REGULAR);
    await injectSession(context, sessionId);

    // Verify settings page shows USD by default.
    await page.goto("/settings");
    await expect(
      page.locator("[data-testid='page-settings']")
    ).toBeVisible();
    const currencySelect = page.locator("#display-currency");
    await expect(currencySelect).toHaveValue("USD");

    // Navigate to performance page and select 5Y to cover seeded price dates.
    await page.goto("/performance");
    await expect(
      page.locator("[data-testid='page-performance']")
    ).toBeVisible();
    await page.getByRole("button", { name: "5Y" }).click();

    // Wait for the chart to render with data.
    const chart = page.locator("[data-testid='chart-container']");
    await expect(chart).toBeVisible({ timeout: 10_000 });

    // Y-axis ticks should contain "$" (USD symbol).
    await expect(chart).toContainText("$");

    // Switch display currency to EUR.
    await page.goto("/settings");
    await expect(currencySelect).toBeVisible();
    await currencySelect.selectOption("EUR");

    // Wait for the "Saved" confirmation.
    await expect(page.getByText("Saved")).toBeVisible({ timeout: 5_000 });

    // Navigate back to performance and verify EUR symbol.
    await page.goto("/performance");
    await page.getByRole("button", { name: "5Y" }).click();
    await expect(chart).toBeVisible({ timeout: 10_000 });

    // Chart should now show EUR symbol instead of USD.
    await expect(chart).toContainText("\u20AC");

    // Cleanup: reset currency to USD.
    await page.goto("/settings");
    await currencySelect.selectOption("USD");
    await expect(page.getByText("Saved")).toBeVisible({ timeout: 5_000 });
  });
});
