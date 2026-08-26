// E2E test: a system archive at a size worth calling a rebuild, and what
// happens when the admin walks away from one.
//
// The size is the test. A two-row archive proves the wiring; it does not
// exercise streaming the export, batching the progress counter, or leave a
// window in which the browser can be closed while the import is still running.
//
// The archive is generated rather than committed: a file this size does not
// belong in the repository, and generating it means the shape is stated in code
// rather than buried in a fixture.

import { test, expect } from "@playwright/test";
import { readFile } from "fs/promises";
import type { Page } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery, INSTRUMENT_NAMES } from "../helpers/db";
import { getJobStatus } from "../helpers/api";
import { writeGeneratedArchive } from "../helpers/archive";
import { JobStatus } from "../gen/api/v1/api_pb";

// 200 instruments x 250 rows. Enough that the import runs for seconds rather
// than milliseconds, which is what makes closing the browser mid-import mean
// anything, and enough that the export has to stream.
const INSTRUMENTS = 200;
const ROWS_EACH = 250;
const TOTAL_ROWS = INSTRUMENTS * ROWS_EACH;

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

/** Reads the id of the newest system archive job straight from the database. */
async function newestArchiveJobId(): Promise<string> {
  const rows = (await rawQuery(
    `SELECT id::text AS id FROM ingestion_jobs
      WHERE job_type = 'system_archive'
      ORDER BY created_at DESC LIMIT 1`,
  )) as { id: string }[];
  expect(rows).toHaveLength(1);
  return rows[0].id;
}

async function startImport(page: Page, file: string): Promise<void> {
  await page.goto("/admin/archive");
  await page.locator("[data-testid='choose-archive-file']").click();
  await page.locator("input[aria-label='Choose archive file']").setInputFiles(file);
  await expect(page.locator("[data-testid='archive-import']")).toContainText("Carries", {
    timeout: TIMEOUT_SLOW,
  });
  await page.locator("[data-testid='start-archive-import']").click();
}

test.describe("system archive at scale", () => {
  let adminSessionId: string;
  let generated: ReturnType<typeof writeGeneratedArchive>;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
    generated = writeGeneratedArchive({
      instruments: INSTRUMENTS,
      rowsEach: ROWS_EACH,
      filename: "scale-archive.json",
    });
  });

  test("an import outlives the browser that started it", async ({ context }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, adminSessionId);
    const page = await context.newPage();
    await startImport(page, generated.path);

    // Wait until the job exists, then close the browser page outright. Not a
    // navigation -- the client is gone.
    await expect(page.locator("[data-testid='archive-job']")).toBeVisible({
      timeout: TIMEOUT_SLOW,
    });
    const jobId = await newestArchiveJobId();
    await page.close();

    // It was genuinely unfinished when the client left, so completing it is
    // something the server did on its own.
    const atClose = await getJobStatus(adminSessionId, jobId);
    expect(atClose.status).not.toBe(JobStatus.SUCCESS);

    // Poll the API with no browser open at all.
    const deadline = Date.now() + 120_000;
    let final = atClose;
    while (Date.now() < deadline && final.status !== JobStatus.SUCCESS && final.status !== JobStatus.FAILED) {
      await new Promise((r) => setTimeout(r, 500));
      final = await getJobStatus(adminSessionId, jobId);
    }
    expect(final.status).toBe(JobStatus.SUCCESS);

    // Every row landed, so what finished was the whole import and not a
    // truncated one.
    const rows = (await rawQuery("SELECT count(*)::int AS n FROM eod_prices")) as { n: number }[];
    expect(rows[0].n).toBe(TOTAL_ROWS);
    const instruments = (await rawQuery(
      `SELECT count(*)::int AS n FROM ${INSTRUMENT_NAMES} ii WHERE ii.value LIKE 'E2E%'`,
    )) as { n: number }[];
    expect(instruments[0].n).toBe(INSTRUMENTS);

    // A returning admin is shown the run they walked away from, because the page
    // looks the job up rather than remembering it.
    const returning = await context.newPage();
    await returning.goto("/admin/archive");
    const parts = returning.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts).toContainText(String(TOTAL_ROWS.toLocaleString()));
    // Two rows, because the uploaded document carries two parts. A part absent
    // from the file is not applied and gets no row -- which is the difference
    // between a part left out and one included but empty.
    await expect(parts.getByText("Done")).toHaveCount(2, { timeout: TIMEOUT_SLOW });
  });

  test("exports the whole instance and the file round trips", async ({ context }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, adminSessionId);
    const page = await context.newPage();
    await page.goto("/admin/archive");

    const downloadPromise = page.waitForEvent("download");
    await page.locator("[data-testid='export-archive']").click();
    const exported = await downloadPromise;
    const file = "/tmp/scale-export.json";
    await exported.saveAs(file);

    // The streamed export reassembled into a document with everything in it.
    // Asserted on the file rather than only on what a re-import produces, so
    // that an export which dropped rows is not mistaken for an import which did.
    const doc = JSON.parse(await readFile(file, "utf8"));
    expect(doc.prices.groups).toHaveLength(INSTRUMENTS);
    const exportedRows = doc.prices.groups.reduce(
      (n: number, g: { rows: unknown[] }) => n + g.rows.length,
      0,
    );
    expect(exportedRows).toBe(TOTAL_ROWS);

    // Re-importing what was just exported is the rebuild the archive exists for,
    // and at this size it is the streaming export and the batched progress
    // counter being exercised rather than described.
    await rawQuery("DELETE FROM eod_prices");
    await startImport(page, file);
    const parts = page.locator("[data-testid='job-parts']");
    // Five rows this time: an export writes every part that was asked for, and
    // the menu ticks all of them but plugin config, so the parts holding
    // nothing are written empty and reported alongside the rest.
    await expect(parts.getByText("Done")).toHaveCount(5, { timeout: TIMEOUT_SLOW });

    const rows = (await rawQuery("SELECT count(*)::int AS n FROM eod_prices")) as { n: number }[];
    expect(rows[0].n).toBe(TOTAL_ROWS);
  });
});
