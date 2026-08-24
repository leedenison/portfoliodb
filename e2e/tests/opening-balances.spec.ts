import { test, expect, type Page } from "@playwright/test";
import { TIMEOUT_FAST } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, seedFixture, closeDB } from "../helpers/db";

// No cassette: nothing here calls a plugin. The instruments are pre-identified by
// the fixture, so no identification runs.

// The seeded AMZN position: 8 shares bought on 2024-01-15.
const AMZN_QTY = 8;
// The pad's declaration: 20 shares held on the buy date, so the opening balance the
// system derives is 12 -- what the user held before their history begins.
const PAD_QTY = 20;
const PAD_DATE = "2024-01-15";
// The assertion: 20 shares still held a few days later. 12 from the pad plus the 8
// bought, so it reconciles.
const ASSERT_QTY = 20;
const ASSERT_DATE = "2024-01-20";

async function openTab(page: Page) {
  await page.goto("/holdings");
  await expect(page.locator("[data-testid='page-holdings']")).toBeVisible({
    timeout: TIMEOUT_FAST,
  });
  await page.locator("[data-testid='tab-opening-balances']").click();
}

async function addCheckpoint(page: Page, qty: number, asOfDate: string) {
  await page.locator("[data-testid='add-declaration']").click();
  await page.locator("[data-testid='declaration-broker']").selectOption("FIDELITY");
  await page.locator("[data-testid='declaration-account']").selectOption("ACC-1");
  await page.locator("[data-testid='declaration-instrument-search']").fill("AMZN");
  await page.locator("[data-testid='declaration-instrument-option']").first().click();
  await page.locator("[data-testid='declaration-qty']").fill(String(qty));
  await page.locator("[data-testid='declaration-as-of-date']").fill(asOfDate);
  await page.locator("[data-testid='declaration-submit']").click();
  await expect(page.locator("[data-testid='declarations-table']")).toBeVisible({
    timeout: TIMEOUT_FAST,
  });
}

test.describe("opening balances: pads and checked assertions", () => {
  let userSession: string;

  test.beforeAll(async () => {
    await resetAndSeedBase();
    await seedFixture("instruments.sql");
    userSession = await seedSession("user");
  });

  test.afterAll(async () => {
    await closeRedis();
    await closeDB();
  });

  test("the earliest checkpoint pads, a later one is checked, and losing a transaction breaks it", async ({
    context,
    page,
  }) => {
    await injectSession(context, userSession);
    await openTab(page);

    // The first checkpoint for the holding seeds its opening balance. It is true by
    // construction -- the system derives a transaction to make it so -- which is why
    // it reports what it is rather than that it passed.
    await addCheckpoint(page, PAD_QTY, PAD_DATE);
    const rows = page.locator("[data-testid='declaration-row']");
    await expect(rows).toHaveCount(1);
    await expect(rows.first().locator("[data-testid='declaration-status']")).toHaveText(
      "Opening balance"
    );
    // A checkpoint is a quantity of one currency line, and the row says which.
    // AMZN has a single line, so the form settled on it without asking.
    await expect(rows.first()).toContainText("(USD)");

    // The declared quantity is now what the holdings page shows, which is the pad
    // doing its job: 12 units from before the history began, plus the 8 bought. The
    // EQUITY counterparty that balances the pad does not net it back to nothing.
    await page.locator("[data-testid='tab-holdings']").click();
    const amzn = page
      .locator("[data-testid='holdings-table'] tbody tr")
      .filter({ hasText: "AMZN" });
    await expect(amzn).toHaveCount(1, { timeout: TIMEOUT_FAST });
    await expect(amzn).toContainText(String(PAD_QTY));

    // A second, later checkpoint generates nothing. It is measured against what the
    // transactions add up to at its own date, and here they agree.
    await openTab(page);
    await addCheckpoint(page, ASSERT_QTY, ASSERT_DATE);
    await expect(rows).toHaveCount(2);
    const assertRow = rows.filter({ hasText: ASSERT_DATE });
    await expect(assertRow.locator("[data-testid='declaration-status']")).toHaveText("Matches");
    await expect(page.locator("[data-testid='declaration-mismatch-badge']")).toHaveCount(0);

    // Lose the AMZN buy, the way a converter that drops a row does. Nothing is
    // recomputed and nothing is invalidated: the check is derived on read, so the
    // next read of it simply disagrees.
    await seedFixture("opening-balances-lose-tx.sql");
    await openTab(page);

    await expect(assertRow.locator("[data-testid='declaration-status']")).toHaveText(
      `Off by -${AMZN_QTY}`
    );
    // The pad still reads as a pad: it was made true by construction and cannot
    // catch anything, which is exactly why the assertion has to exist.
    await expect(
      rows.filter({ hasText: PAD_DATE }).locator("[data-testid='declaration-status']")
    ).toHaveText("Opening balance");

    // A user not looking at the tab still learns there is something to look at.
    await expect(page.locator("[data-testid='declaration-mismatch-badge']")).toHaveText("1");
  });
});
