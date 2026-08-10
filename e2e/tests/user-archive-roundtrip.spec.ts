// E2E test: the user archive page.
//
// Exports a user's own archive through the UI, wipes what it carried, imports
// the same file back and then checks the database. The round trip is the point:
// a file this instance produced has to be one it can consume. Preferences are
// settings nothing can recover -- a rebuild that silently reset the display
// currency to the column default would be a rebuild that changed the user's
// data -- and transactions are the data a rebuild exists for.
//
// The preferences are written through the RPCs rather than through SQL, so what
// the export reads is the shape the API stores. The transactions come from a
// fixture because seeding them through an upload would mean identifying them,
// which is a paid lookup and a cassette this suite has no other need of.

import { test, expect } from "@playwright/test";
import path from "path";
import { readFile, writeFile } from "fs/promises";
import { tmpdir } from "os";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import {
  seedSession,
  injectSession,
  closeRedis,
  TEST_USER_ID,
} from "../helpers/auth";
import {
  resetAndSeedBase,
  closeDB,
  rawQuery,
  seedFixture,
} from "../helpers/db";
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

  // Three balanced groups under one broker: a priced BUYSTOCK and the EQUITY
  // counterparty that makes it sum to zero.
  test.beforeAll(async () => {
    await seedFixture("user-archive-txs.sql");
  });

  test("exports the user's preferences and imports them back", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);
    await page.goto("/archive");
    await expect(
      page.getByRole("heading", { name: "Your archive" }),
    ).toBeVisible();

    const preferences = page.getByLabel(/Preferences/);
    await expect(preferences).toBeChecked();
    // Both parts are on by default, which is what a rebuild wants. This test is
    // about the settings, so it asks for them alone and the assertions below
    // can be exact about what the document holds.
    await page.getByLabel(/Transactions/).uncheck();

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
    // No system data reaches a user archive, and a part left out of the menu is
    // absent rather than present and empty.
    expect(doc.instruments).toBeUndefined();
    expect(doc.prices).toBeUndefined();
    expect(doc.txs).toBeUndefined();

    // Put both settings back to what they were, so what lands afterwards came
    // from the file rather than from what was already there.
    await rawQuery(`UPDATE users SET display_currency = 'USD' WHERE id = $1`, [
      TEST_USER_ID,
    ]);
    await rawQuery(`DELETE FROM ignored_asset_classes WHERE user_id = $1`, [
      TEST_USER_ID,
    ]);

    await page.locator("[data-testid='choose-archive-file']").click();
    await page
      .locator("input[aria-label='Choose archive file']")
      .setInputFiles(exported);
    // Preferences counts settings rather than rows, so the preview says two.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "Carries 2 preference settings",
    );
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Done")).toHaveCount(1, {
      timeout: TIMEOUT_SLOW,
    });
    // Both settings applied, none rejected.
    await expect(parts).toContainText("2 / 2");

    // What the page said happened, checked against what is stored.
    const users = (await rawQuery(
      `SELECT display_currency FROM users WHERE id = $1`,
      [TEST_USER_ID],
    )) as { display_currency: string }[];
    expect(users[0].display_currency).toBe(CURRENCY);

    const rules = (await rawQuery(
      `SELECT broker, account, asset_class FROM ignored_asset_classes WHERE user_id = $1`,
      [TEST_USER_ID],
    )) as { broker: string; account: string; asset_class: string }[];
    expect(rules).toEqual([
      { broker: "IBKR", account: "U123", asset_class: "OPTION" },
    ]);
  });

  test("exports the user's transactions and imports them back", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);
    await page.goto("/archive");

    await page.getByLabel(/Preferences/).uncheck();
    await expect(page.getByLabel(/Transactions/)).toBeChecked();

    const downloadPromise = page.waitForEvent("download");
    await page.locator("[data-testid='export-archive']").click();
    const exported = path.join(tmpdir(), "user-archive-txs-e2e.json");
    await (await downloadPromise).saveAs(exported);
    const doc = JSON.parse(await readFile(exported, "utf8"));

    // One window per broker, bounded by the postings it carries, so the window
    // provably contains all of them.
    expect(doc.txs.windows).toHaveLength(1);
    const window = doc.txs.windows[0];
    expect(window.broker).toBe("FIDELITY");
    // The window names the export rather than an ingestion job: a window carries
    // one source and a broker's postings can come from several.
    expect(window.source).toBe("FIDELITY:archive:export");
    expect(window.period_from).toBe("2024-01-15T00:00:00Z");
    expect(window.period_before).toBe("2024-01-18T00:00:00Z");

    // Postings are flat and the grouping is a key on them, numbered within the
    // window rather than being the stored id. Three trades, each with the leg
    // that balances it. See
    // docs/adr/0043-grouping-does-not-travel-in-the-archive.md.
    const postings = window.postings as Record<string, unknown>[];
    expect(postings).toHaveLength(6);
    const byRef = new Map<string, Record<string, unknown>[]>();
    for (const p of postings) {
      const ref = p.group_ref as string;
      byRef.set(ref, [...(byRef.get(ref) ?? []), p]);
    }
    expect([...byRef.keys()].sort()).toEqual(["g0", "g1", "g2"]);
    for (const legs of byRef.values()) {
      expect(legs).toHaveLength(2);
    }
    // Order within a group carries no meaning -- the legs of a trade share a
    // timestamp, so nothing stored distinguishes them -- and neither the file
    // nor a reader depends on it.
    const first = postings.find(
      (p) =>
        typeof p.instrument_description === "string" &&
        p.instrument_description.startsWith("AMZN"),
    ) as Record<string, unknown>;
    // Decimals are strings, never JSON numbers: a double cannot carry an exact
    // decimal and a value that does not reimport identically is a bug.
    expect(first.quantity).toBe("8");
    expect(first.unit_price).toBe("155.2");
    // Named by identifier and never by id, and by the one bestIdentifierJoin
    // picks: MIC_TICKER outranks the ISIN the same instrument also carries.
    expect(first.identifier_hints).toEqual([
      { type: "MIC_TICKER", value: "AMZN" },
    ]);
    // The counterparty travels with its account type, so the group still sums to
    // zero on the way back in and nothing is routed a second time.
    //
    // Spelled with its prefix, unlike every other vocabulary in the format.
    // AccountType cannot be unprefixed the way AssetClass was: enum values share
    // package scope and TxType already defines INCOME and TRANSFER.
    expect(
      postings.filter((p) => p.account_type === "ACCOUNT_TYPE_EQUITY"),
    ).toHaveLength(3);
    // Derived state is not carried: the split-adjusted pair is a recomputable
    // cache and the weights are computed at ingest.
    expect(first.split_adjusted_quantity).toBeUndefined();
    expect(first.weight).toBeUndefined();

    // Clear what the file carried, so what lands afterwards came from the file
    // rather than from what was already there. Deleting the groups takes their
    // postings with them.
    await rawQuery(`DELETE FROM tx_groups WHERE user_id = $1`, [TEST_USER_ID]);

    await page.locator("[data-testid='choose-archive-file']").click();
    await page
      .locator("input[aria-label='Choose archive file']")
      .setInputFiles(exported);
    // Postings rather than windows, so the preview and the job's own total
    // agree.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "Carries 6 postings",
    );
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Done")).toHaveCount(1, {
      timeout: TIMEOUT_SLOW,
    });
    await expect(parts).toContainText("6 / 6");

    // Every posting back, in three groups, with no residual routed on top: the
    // groups the file carried already balanced.
    const restored = (await rawQuery(
      `SELECT t.instrument_description, t.tx_type, t.quantity, t.unit_price, t.account_type,
              t.weight, t.weight_commodity, t.group_id
       FROM txs t WHERE t.user_id = $1 ORDER BY t.timestamp, t.account_type`,
      [TEST_USER_ID],
    )) as {
      instrument_description: string;
      quantity: string;
      account_type: string;
      weight: string;
      weight_commodity: string;
      group_id: string;
    }[];
    expect(restored).toHaveLength(6);
    expect(new Set(restored.map((r) => r.group_id)).size).toBe(3);
    expect(restored.filter((r) => r.account_type === "IMBALANCE")).toHaveLength(
      0,
    );

    // The weights are recomputed from the raw columns rather than carried, and
    // they come back the same, which is what lets the group balance again.
    const amzn = restored.find((r) =>
      r.instrument_description.startsWith("AMZN"),
    );
    expect(amzn?.quantity).toBe("8");
    // Compared as a number: the seeded row kept the scale raw SQL wrote it with
    // and the recomputed one does not, which is the trailing zero the format
    // says it does not preserve.
    expect(Number(amzn?.weight)).toBe(1241.6);
    expect(amzn?.weight_commodity).toBe("cur:USD");

    // Every group sums to zero per commodity, which the balance constraint
    // enforced at COMMIT and which is the invariant the archive has to preserve.
    const unbalanced = (await rawQuery(
      `SELECT group_id FROM txs WHERE user_id = $1
       GROUP BY group_id, weight_commodity HAVING SUM(weight) <> 0`,
      [TEST_USER_ID],
    )) as unknown[];
    expect(unbalanced).toHaveLength(0);
  });

  test("refuses a file that is not a user archive", async ({
    context,
    page,
  }) => {
    await injectSession(context, sessionId);
    await page.goto("/archive");

    const systemArchive = path.join(
      tmpdir(),
      "system-archive-on-user-page.json",
    );
    await writeFile(
      systemArchive,
      JSON.stringify({
        envelope: {
          format_version: 1,
          exported_at: "2026-07-30T00:00:00Z",
          kind: "SYSTEM",
        },
      }),
    );
    await page.locator("[data-testid='choose-archive-file']").click();
    await page
      .locator("input[aria-label='Choose archive file']")
      .setInputFiles(systemArchive);

    // Which archive a file is is answered at parse time, so this never reaches
    // a job and the two archives cannot be crossed by accident.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "This is a system archive",
    );
    await expect(
      page.locator("[data-testid='start-archive-import']"),
    ).toHaveCount(0);
  });
});
