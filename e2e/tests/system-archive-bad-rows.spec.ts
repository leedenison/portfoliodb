// E2E test: what an import does with data it cannot use.
//
// Two different failures, and the difference between them is the point. A row
// the importer cannot read is rejected on its own and the part still succeeds,
// so a mostly-good archive still lands. A field that does not match its declared
// shape is refused for the whole document before any of it is applied, because
// whether a file is a valid archive is an all-or-nothing question.

import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery } from "../helpers/db";
import { writeGeneratedArchive } from "../helpers/archive";

const INSTRUMENTS = 2;
const ROWS_EACH = 5;

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("system archive import with unusable data", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
  });

  test("rejects the bad row, applies the rest, and says how many it dropped", async ({
    context,
    page,
  }) => {
    // One impossible date in the first instrument's second row. It matches the
    // field's pattern, so it reaches the importer rather than being refused at
    // the edge.
    const gen = writeGeneratedArchive({
      instruments: INSTRUMENTS,
      rowsEach: ROWS_EACH,
      badDates: [[0, 1]],
      filename: "bad-row-archive.json",
    });

    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(gen.path);
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    // Both parts finish. A rejected row is not a failed part -- what failed is a
    // row, and the result reads "done, one rejected" rather than "failed".
    await expect(parts.getByText("Done")).toHaveCount(2, { timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Failed")).toHaveCount(0);

    // The count is on the Prices row, and the problem is named underneath it.
    const priceRow = parts.locator("tr", { hasText: "Prices" });
    await expect(priceRow).toContainText("1");
    await expect(page.locator("[data-testid='archive-job']")).toContainText("price_date");

    // Everything except the bad row landed, which is what makes rejecting a row
    // rather than the file worth doing.
    const rows = (await rawQuery("SELECT count(*)::int AS n FROM eod_prices")) as { n: number }[];
    expect(rows[0].n).toBe(INSTRUMENTS * ROWS_EACH - 1);
  });

  test("refuses the whole file when a field does not match its declared shape", async ({
    context,
    page,
  }) => {
    await rawQuery("DELETE FROM eod_prices");
    const before = (await rawQuery(
      "SELECT count(*)::int AS n FROM ingestion_jobs WHERE job_type = 'system_archive'",
    )) as { n: number }[];
    const gen = writeGeneratedArchive({
      instruments: INSTRUMENTS,
      rowsEach: ROWS_EACH,
      badDecimals: [[1, 2]],
      filename: "bad-decimal-archive.json",
    });

    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(gen.path);
    await page.locator("[data-testid='start-archive-import']").click();

    // The upload is refused rather than queued, so the page reports it where the
    // upload happened. The offending field is named, which is the difference
    // between "this file is wrong" and "this file is wrong here".
    await expect(page.locator("[data-testid='archive-import']")).toContainText(/close/i, {
      timeout: TIMEOUT_SLOW,
    });

    // No job was created. The results panel still shows the previous run, which
    // is right -- a refused upload does not erase the last import -- so counting
    // jobs is what says nothing was queued.
    const after = (await rawQuery(
      "SELECT count(*)::int AS n FROM ingestion_jobs WHERE job_type = 'system_archive'",
    )) as { n: number }[];
    expect(after[0].n).toBe(before[0].n);

    // Nothing from a refused document is applied, including the rows that were
    // perfectly good.
    const rows = (await rawQuery("SELECT count(*)::int AS n FROM eod_prices")) as { n: number }[];
    expect(rows[0].n).toBe(0);
  });
});
