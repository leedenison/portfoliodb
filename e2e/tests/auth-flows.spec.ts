import { test, expect } from "@playwright/test";
import {
  seedSession,
  injectSession,
  deleteSession,
  closeRedis,
} from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("unauthenticated user", () => {
  test("sees sign-in page at root", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("[data-testid='page-signin']")).toBeVisible();
  });
});

test.describe("authenticated user", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("root redirects to holdings", async ({ context, page }) => {
    await injectSession(context, sessionId);
    await page.goto("/");
    await expect(page).toHaveURL(/\/holdings/);
    await expect(
      page.locator("[data-testid='page-holdings']")
    ).toBeVisible();
  });

  test("can navigate to all main pages", async ({ context, page }) => {
    await injectSession(context, sessionId);

    const pages = [
      { path: "/holdings", testid: "page-holdings" },
      { path: "/transactions", testid: "page-transactions" },
      { path: "/uploads", testid: "page-uploads" },
      { path: "/performance", testid: "page-performance" },
      { path: "/settings", testid: "page-settings" },
    ];

    for (const p of pages) {
      await page.goto(p.path);
      await expect(
        page.locator(`[data-testid='${p.testid}']`)
      ).toBeVisible();
    }
  });
});

test.describe("admin user", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("admin");
  });

  test("can access admin pages", async ({ context, page }) => {
    await injectSession(context, sessionId);
    await page.goto("/admin/prices");
    await expect(
      page.locator("[data-testid='prices-table']")
    ).toBeVisible({ timeout: 10_000 });
  });
});

test.describe("session expiry", () => {
  test("shows unauthenticated state when session is deleted", async ({
    context,
    page,
  }) => {
    const sessionId = await seedSession("user");
    await injectSession(context, sessionId);

    // Verify we are authenticated.
    await page.goto("/holdings");
    await expect(
      page.locator("[data-testid='page-holdings']")
    ).toBeVisible();

    // Delete the session from Redis (simulates server-side expiry).
    await deleteSession(sessionId);

    // Reload the page. GetSession() will fail (session gone from Redis),
    // triggering SessionLostHandler which redirects to the sign-in page.
    await page.reload();
    await expect(
      page.locator("[data-testid='page-signin']")
    ).toBeVisible({ timeout: 10_000 });
  });
});
