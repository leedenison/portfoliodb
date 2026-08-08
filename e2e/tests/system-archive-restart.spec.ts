// E2E test: an import interrupted by the service restarting.
//
// The archive exists so that a rebuild does not have to be paid for twice, and a
// rebuild is long enough that the service can be restarted in the middle of one.
// What has to survive that is the job: its payload, which is the only copy of
// what was being imported, and the record of which parts had already been
// applied.
//
// This is the path the payload used to be lost on. Clearing it at the start of a
// job meant a restart re-enqueued a row with nothing in it, which unmarshalled
// cleanly as an empty document, imported nothing, and reported success.

import { test, expect } from "@playwright/test";
import { seedSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery } from "../helpers/db";
import { restartServer } from "../helpers/cassette";
import { getJobStatus, importSystemArchive } from "../helpers/api";
import { writeGeneratedArchive, readArchive } from "../helpers/archive";
import { JobStatus } from "../gen/api/v1/api_pb";

// Big enough that the import is still running when the process is replaced.
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

/** How many price rows have been written so far. */
async function storedRows(): Promise<number> {
  const rows = (await rawQuery("SELECT count(*)::int AS n FROM eod_prices")) as { n: number }[];
  return rows[0].n;
}

test.describe("system archive import across a restart", () => {
  test("resumes and finishes an import the service was killed in the middle of", async () => {
    test.setTimeout(240_000);
    const admin = await seedSession("admin");
    const gen = writeGeneratedArchive({
      instruments: INSTRUMENTS,
      rowsEach: ROWS_EACH,
      filename: "restart-archive.json",
    });

    const jobId = await importSystemArchive(admin, readArchive(gen.path));

    // Wait until the import is demonstrably under way: the instrument part has
    // finished and prices have started landing. Restarting before that would
    // test re-enqueueing a job that had not begun, which is a weaker claim.
    const startedBy = Date.now() + 60_000;
    let progressed = 0;
    while (Date.now() < startedBy) {
      progressed = await storedRows();
      if (progressed > 0) break;
      await new Promise((r) => setTimeout(r, 100));
    }
    expect(progressed).toBeGreaterThan(0);
    expect(progressed).toBeLessThan(TOTAL_ROWS);

    // Replace the process. Everything the import was holding goes with it.
    await restartServer();

    // The job was left mid-flight, so nothing had marked it terminal.
    const afterRestart = await getJobStatus(admin, jobId);
    expect([JobStatus.PENDING, JobStatus.RUNNING]).toContain(afterRestart.status);

    // The new process finds the job and finishes it. Nothing here nudges it --
    // no upload, no retry, no client -- so completing is entirely recovery.
    const deadline = Date.now() + 180_000;
    let final = afterRestart;
    while (
      Date.now() < deadline &&
      final.status !== JobStatus.SUCCESS &&
      final.status !== JobStatus.FAILED
    ) {
      await new Promise((r) => setTimeout(r, 500));
      final = await getJobStatus(admin, jobId);
    }
    expect(final.status).toBe(JobStatus.SUCCESS);

    // Every row is present exactly once. The parts are built from idempotent
    // upserts, so re-running one must land the same data rather than double it.
    expect(await storedRows()).toBe(TOTAL_ROWS);
    const instruments = (await rawQuery(
      "SELECT count(*)::int AS n FROM instrument_identifiers WHERE value LIKE 'E2E%'",
    )) as { n: number }[];
    expect(instruments[0].n).toBe(INSTRUMENTS);

    // Every part reports done, and the counts are the whole part rather than
    // what was left when the process went.
    expect(final.parts).toHaveLength(2);
    for (const part of final.parts) {
      expect(part.status).toBe(JobStatus.SUCCESS);
      expect(part.processedCount).toBe(part.totalCount);
    }
  });
});
