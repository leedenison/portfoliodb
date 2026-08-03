import { test, expect } from "@playwright/test";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, seedFixture, closeDB } from "../helpers/db";

test.beforeAll(async () => {
  await resetAndSeedBase();
  await seedFixture("residual-balances.sql");
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("admin imbalance page", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
  });

  test("reports imbalances by broker, split by the event that left them", async ({
    context,
    page,
  }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/imbalance");

    await expect(page.locator("[data-testid='page-imbalance']")).toBeVisible({
      timeout: 10_000,
    });

    const table = page.locator("[data-testid='imbalance-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });

    // The uncategorised dividend and the unreported fee are separate rows: they
    // lead to different converter work.
    const rows = page.locator("[data-testid='imbalance-row']");
    await expect(rows).toHaveCount(2);
    await expect(rows.filter({ hasText: "Income" })).toHaveCount(1);
    await expect(rows.filter({ hasText: "Buy Stock" })).toHaveCount(1);

    // One broker, and its subtotal nets the two.
    await expect(page.locator("[data-testid='imbalance-broker-group']")).toHaveCount(1);
    await expect(page.locator("[data-testid='imbalance-subtotal']")).toContainText("132.13");
  });

  test("lists every transfer balance, aged, until matching lands", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/imbalance");
    await expect(page.locator("[data-testid='page-imbalance']")).toBeVisible({
      timeout: 10_000,
    });

    await page.getByRole("button", { name: "Unmatched transfers" }).click();
    await expect(page.locator("[data-testid='transfers-table']")).toBeVisible();

    // Nothing pairs the two sides of a journal yet, so the completed Schwab
    // transfer is listed alongside the two IBKR sides rather than being resolved
    // away. The page says so rather than implying all four need attention.
    const rows = page.locator("[data-testid='transfer-row']");
    await expect(rows).toHaveCount(4);
    await expect(rows.filter({ hasText: "SCH-" })).toHaveCount(2);
    await expect(
      page.getByText("These are every imported transfer, not the unmatched ones.")
    ).toBeVisible();

    // The age shown is the age of the posting.
    await expect(rows.filter({ hasText: "U-OLD" })).toHaveAttribute("data-age-bucket", "loud");
    await expect(rows.filter({ hasText: "U-NEW" })).toHaveAttribute("data-age-bucket", "fresh");
  });

  test("dashboard card summarises what needs attention", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin");

    // Scoped past the sidebar link to the same route.
    const card = page.locator("a[href='/admin/imbalance']").filter({ hasText: "how lossy" });
    await expect(card).toBeVisible({ timeout: 10_000 });
    // Two imbalanced keys. The transfer count is stated, not flagged.
    await expect(card).toContainText("2 imbalanced, 3 transfers in flight over 7d");
  });

  test("non-admin user sees access denied", async ({ context, page }) => {
    const userSessionId = await seedSession("user");
    await injectSession(context, userSessionId);
    await page.goto("/admin/imbalance");
    await expect(page.getByText("Access denied")).toBeVisible();
  });
});
