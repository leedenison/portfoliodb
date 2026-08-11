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
    await expect(rows.filter({ hasText: "Trade (Asset)" })).toHaveCount(1);

    // One broker, and its subtotal nets the two.
    await expect(page.locator("[data-testid='imbalance-broker-group']")).toHaveCount(1);
    await expect(page.locator("[data-testid='imbalance-subtotal']")).toContainText("132.13");
  });

  test("lists only the transfers whose second side never arrived", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/imbalance");
    await expect(page.locator("[data-testid='page-imbalance']")).toBeVisible({
      timeout: 10_000,
    });

    await page.getByRole("button", { name: "Unmatched transfers" }).click();
    await expect(page.locator("[data-testid='transfers-table']")).toBeVisible();

    // The Schwab journal is matched, so both of its sides are settled and drop
    // out; what is left is the two IBKR sides whose counterparts never arrived.
    const rows = page.locator("[data-testid='transfer-row']");
    await expect(rows).toHaveCount(2);
    await expect(rows.filter({ hasText: "SCH-" })).toHaveCount(0);
    // The caveat that this listed every imported transfer is gone with the reason
    // for it.
    await expect(
      page.getByText("These are every imported transfer, not the unmatched ones.")
    ).toHaveCount(0);

    // The age shown is now the age of a missing side.
    await expect(rows.filter({ hasText: "U-OLD" })).toHaveAttribute("data-age-bucket", "loud");
    await expect(rows.filter({ hasText: "U-NEW" })).toHaveAttribute("data-age-bucket", "fresh");
  });

  test("dashboard card summarises what needs attention", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin");

    // Scoped past the sidebar link to the same route.
    const card = page.locator("a[href='/admin/imbalance']").filter({ hasText: "how lossy" });
    await expect(card).toBeVisible({ timeout: 10_000 });
    // Two imbalanced keys, and one transfer whose second side never arrived: the
    // settled Schwab pair is matched and no longer counted. Both now flag.
    await expect(card).toContainText("2 imbalanced, 1 unmatched over 7d");
  });

  test("non-admin user sees access denied", async ({ context, page }) => {
    const userSessionId = await seedSession("user");
    await injectSession(context, userSessionId);
    await page.goto("/admin/imbalance");
    await expect(page.getByText("Access denied")).toBeVisible();
  });
});
