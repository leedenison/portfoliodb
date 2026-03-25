import { test, expect } from "@playwright/test";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("admin workers page", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
  });

  test("displays registered workers with idle state", async ({
    context,
    page,
  }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/workers");

    await expect(
      page.locator("[data-testid='page-workers']")
    ).toBeVisible({ timeout: 10_000 });

    // Workers table should appear with at least one row.
    const table = page.locator("[data-testid='workers-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    const rows = page.locator("[data-testid='worker-row']");
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);

    // At startup with no jobs running, all workers should be idle.
    const states = page.locator("[data-worker-state]");
    const stateValues = await states.evaluateAll((els) =>
      els.map((el) => el.getAttribute("data-worker-state"))
    );
    expect(stateValues.length).toBeGreaterThan(0);
    for (const state of stateValues) {
      expect(state).toBe("idle");
    }
  });

  test("non-admin user sees access denied", async ({ context, page }) => {
    const userSessionId = await seedSession("user");
    await injectSession(context, userSessionId);
    await page.goto("/admin/workers");
    await expect(page.getByText("Access denied")).toBeVisible();
  });
});
