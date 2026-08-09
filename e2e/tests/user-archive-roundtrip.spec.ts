// E2E test: the user archive page.
//
// Exports a user's own archive through the UI, wipes what it carried, imports
// the same file back and then checks the database. The round trip is the point:
// a file this instance produced has to be one it can consume, and preferences
// are the settings nothing can recover -- a rebuild that silently reset the
// display currency to the column default would be a rebuild that changed the
// user's data.
//
// The seeded state is written through the RPCs rather than through SQL, so what
// the export reads is the shape the API stores.

import { test, expect } from "@playwright/test";
import path from "path";
import { readFile, writeFile } from "fs/promises";
import { tmpdir } from "os";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis, TEST_USER_ID } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery } from "../helpers/db";
import { setDisplayCurrency, setIgnoredAssetClasses } from "../helpers/api";
import { AssetClass } from "../gen/type/v1/type_pb";

const CURRENCY = "GBP";
const RULE = { broker: "IBKR", account: "U123", assetClass: AssetClass.OPTION };

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("user archive page", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
    await setDisplayCurrency(sessionId, CURRENCY);
    await setIgnoredAssetClasses(sessionId, [RULE]);
  });

  test("exports the user's preferences and imports them back", async ({ context, page }) => {
    await injectSession(context, sessionId);
    await page.goto("/archive");
    await expect(page.getByRole("heading", { name: "Your archive" })).toBeVisible();

    const preferences = page.getByLabel(/Preferences/);
    await expect(preferences).toBeChecked();

    const downloadPromise = page.waitForEvent("download");
    await page.locator("[data-testid='export-archive']").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("user-archive.json");

    const exported = path.join(tmpdir(), "user-archive-e2e.json");
    await download.saveAs(exported);
    const doc = JSON.parse(await readFile(exported, "utf8"));

    // The envelope says which of the two archives this is, and the settings are
    // carried with their values -- not merely present and empty, which is what
    // an export that silently dropped them would also look like.
    expect(doc.envelope.kind).toBe("USER");
    expect(doc.envelope.format_version).toBe(1);
    expect(doc.preferences.display_currency).toBe(CURRENCY);
    // Broker and asset class are enums in the file and strings in the column.
    expect(doc.preferences.ignored_asset_classes.rules).toEqual([
      { broker: "IBKR", account: "U123", asset_class: "OPTION" },
    ]);
    // No system data reaches a user archive.
    expect(doc.instruments).toBeUndefined();
    expect(doc.prices).toBeUndefined();

    // Put both settings back to what they were, so what lands afterwards came
    // from the file rather than from what was already there.
    await rawQuery(`UPDATE users SET display_currency = 'USD' WHERE id = $1`, [TEST_USER_ID]);
    await rawQuery(`DELETE FROM ignored_asset_classes WHERE user_id = $1`, [TEST_USER_ID]);

    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(exported);
    // Preferences counts settings rather than rows, so the preview says two.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "Carries 2 preference settings",
    );
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Done")).toHaveCount(1, { timeout: TIMEOUT_SLOW });
    // Both settings applied, none rejected.
    await expect(parts).toContainText("2 / 2");

    // What the page said happened, checked against what is stored.
    const users = (await rawQuery(`SELECT display_currency FROM users WHERE id = $1`, [
      TEST_USER_ID,
    ])) as { display_currency: string }[];
    expect(users[0].display_currency).toBe(CURRENCY);

    const rules = (await rawQuery(
      `SELECT broker, account, asset_class FROM ignored_asset_classes WHERE user_id = $1`,
      [TEST_USER_ID],
    )) as { broker: string; account: string; asset_class: string }[];
    expect(rules).toEqual([{ broker: "IBKR", account: "U123", asset_class: "OPTION" }]);
  });

  test("refuses a file that is not a user archive", async ({ context, page }) => {
    await injectSession(context, sessionId);
    await page.goto("/archive");

    const systemArchive = path.join(tmpdir(), "system-archive-on-user-page.json");
    await writeFile(
      systemArchive,
      JSON.stringify({
        envelope: { format_version: 1, exported_at: "2026-07-30T00:00:00Z", kind: "SYSTEM" },
      }),
    );
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(systemArchive);

    // Which archive a file is is answered at parse time, so this never reaches
    // a job and the two archives cannot be crossed by accident.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "This is a system archive",
    );
    await expect(page.locator("[data-testid='start-archive-import']")).toHaveCount(0);
  });
});
