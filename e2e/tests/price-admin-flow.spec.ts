import { test, expect } from "@playwright/test";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";

test.afterAll(async () => {
  await closeRedis();
});

test.describe("admin prices page", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
  });

  test("shows seeded price data in prices table", async ({
    context,
    page,
  }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/prices");

    // The prices table should be visible with seeded data.
    const table = page.locator("[data-testid='prices-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    // Verify at least some rows exist (seeded 15 price rows across 3 instruments).
    const rows = table.locator("tbody tr");
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);
  });

  test("non-admin user sees access denied", async ({ context, page }) => {
    const userSessionId = await seedSession("user");
    await injectSession(context, userSessionId);
    await page.goto("/admin/prices");

    // Admin layout shows "Access denied" for non-admin users.
    await expect(page.getByText("Access denied")).toBeVisible();
  });
});

test.describe("admin navigation", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
  });

  test("can navigate admin sidebar pages", async ({ context, page }) => {
    await injectSession(context, adminSessionId);

    // Instruments page.
    await page.goto("/admin/instruments");
    await expect(page.getByRole("heading", { name: "Instruments" })).toBeVisible();

    // Plugins pages.
    await page.goto("/admin/plugins/identifier");
    await expect(page.getByRole("heading", { name: "Identifier" })).toBeVisible();

    await page.goto("/admin/plugins/price");
    await expect(page.getByRole("heading", { name: "Price" })).toBeVisible();

    // Telemetry page.
    await page.goto("/admin/telemetry");
    await expect(page.getByRole("heading", { name: "Telemetry" })).toBeVisible();
  });
});

test.describe("performance chart with seeded data", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("renders valuation chart when prices exist", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);
    await page.goto("/performance");
    await expect(
      page.locator("[data-testid='page-performance']")
    ).toBeVisible();

    // Seeded prices are from January 2024. Select the 5Y period to ensure
    // the date range covers the seeded data.
    await page.getByRole("button", { name: "5Y" }).click();

    // Wait for the chart container to appear (only renders when points > 0).
    await expect(
      page.locator("[data-testid='chart-container']")
    ).toBeVisible({ timeout: 15_000 });

    // Verify the chart has rendered SVG content (Recharts uses SVG).
    const svg = page.locator("[data-testid='chart-container'] svg");
    await expect(svg).toBeVisible();
  });
});
