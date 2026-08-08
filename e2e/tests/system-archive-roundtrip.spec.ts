// E2E test: the consolidated archive page.
//
// Exports a system archive through the UI, imports the same file back, and then
// checks the database. The round trip is the point: a file this instance
// produced has to be one it can consume, and the per-part results are what tell
// an admin what an import actually applied.
//
// The seed is loaded as an archive carrying an instrument part rather than
// prices alone, so the exported file has instruments in it. Two rows the export
// used to drop are seeded directly: an instrument whose asset_class is still
// NULL -- one a price import created before identification reached it -- and a
// provider identifier on a seeded FX pair. Both are what a rebuild cannot
// reconstruct, and the FX pair is also the collision every rebuild hits, since
// migration 002 has already created it on the importing instance.

import { test, expect } from "@playwright/test";
import path from "path";
import { readFile, writeFile } from "fs/promises";
import { tmpdir } from "os";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery } from "../helpers/db";
import { importSystemArchiveAndWait } from "../helpers/api";
import { writeGeneratedArchive, readArchive } from "../helpers/archive";
import { JobStatus } from "../gen/api/v1/api_pb";

// Two instruments with three price rows each: small enough to assert on every
// value, large enough to have a second group.
const SEED = { instruments: 2, rowsEach: 3 };

// An instrument with no asset class, and a currency pair migration 002 seeds on
// every instance -- the two cases a rebuild has to carry through the file.
const UNCLASSIFIED_TICKER = "NOCLASS1";
const FX_PAIR = "EURUSD";

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("system archive page", () => {
  let adminSessionId: string;
  let tickers: string[];

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
    const seed = writeGeneratedArchive({ ...SEED, filename: "roundtrip-seed.json" });
    tickers = seed.tickers;
    const job = await importSystemArchiveAndWait(adminSessionId, readArchive(seed.path));
    expect(job.status).toBe(JobStatus.SUCCESS);

    // An instrument identification has not classified yet. Created directly
    // because no ingest path reaches this state without calling a plugin.
    await rawQuery(
      `WITH i AS (INSERT INTO instruments (currency) VALUES ('USD') RETURNING id)
       INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical)
       SELECT id, 'MIC_TICKER', 'XNAS', $1, true FROM i`,
      [UNCLASSIFIED_TICKER],
    );
    // The recorded output of a lookup, hung on reference data the importing
    // instance already has.
    await rawQuery(
      `INSERT INTO provider_instrument_identifiers (instrument_id, provider, identifier_type, value)
       SELECT ii.instrument_id, 'eodhd', 'EODHD_EXCH_CODE', 'FOREX'
         FROM instrument_identifiers ii
        WHERE ii.identifier_type = 'FX_PAIR' AND ii.value = $1
       ON CONFLICT DO NOTHING`,
      [FX_PAIR],
    );
  });

  test("exports a file and imports it back, preserving the data", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");
    await expect(page.getByRole("heading", { name: "Archive" })).toBeVisible();

    // Parts the format does not carry yet are visible but not selectable, so the
    // menu says what the archive will hold rather than only what it holds today.
    await expect(page.getByLabel(/Plugin config/)).toBeDisabled();

    const downloadPromise = page.waitForEvent("download");
    await page.locator("[data-testid='export-archive']").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("system-archive.json");

    const exported = path.join(tmpdir(), "system-archive-e2e.json");
    await download.saveAs(exported);
    const doc = JSON.parse(await readFile(exported, "utf8"));

    // The envelope says what the file is, and every part asked for is carried
    // with its contents -- not merely present and empty, which is what an
    // export that silently dropped its rows would also look like.
    expect(doc.envelope.kind).toBe("SYSTEM");
    expect(doc.envelope.format_version).toBe(1);
    const exportedTickers = doc.instruments.instruments
      .flatMap((i: { identifiers: { value: string }[] }) => i.identifiers.map((id) => id.value))
      .filter((v: string) => v.startsWith("E2E"));
    expect(exportedTickers.sort()).toEqual([...tickers].sort());
    expect(doc.prices.groups).toHaveLength(SEED.instruments);
    expect(doc.prices.groups[0].rows).toHaveLength(SEED.rowsEach);

    type ExportedInstrument = {
      asset_class?: string;
      identifiers: { value: string }[];
      provider_identifiers?: { provider: string; identifier_type: string; value: string }[];
    };
    const exportedInstruments = doc.instruments.instruments as ExportedInstrument[];
    const named = (v: string) =>
      exportedInstruments.find((i) => i.identifiers.some((id) => id.value === v));

    // An instrument with no asset class is exactly what a rebuild cannot
    // reconstruct, so an export that quietly dropped it would be worse than
    // useless.
    expect(named(UNCLASSIFIED_TICKER)).toBeDefined();
    // FX pairs are instruments, and the lookup output hung on one is the thing
    // the archive exists to avoid paying for twice.
    const fx = named(FX_PAIR);
    expect(fx?.provider_identifiers).toEqual([
      expect.objectContaining({ provider: "eodhd", identifier_type: "EODHD_EXCH_CODE", value: "FOREX" }),
    ]);
    // Coverage is not derivable from the rows, so losing it would be silent.
    expect(doc.prices.groups[0].coverage).toHaveLength(1);

    // Wipe the price rows and re-import the file, so what lands afterwards came
    // from the archive rather than from what was already there.
    await rawQuery("DELETE FROM eod_prices");
    await rawQuery("DELETE FROM price_coverage");

    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(exported);
    await expect(page.locator("[data-testid='archive-import']")).toContainText("Carries");
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Done")).toHaveCount(3, { timeout: TIMEOUT_SLOW });

    // What the page said happened, checked against what is stored.
    const priceRows = (await rawQuery(
      `SELECT count(*)::int AS n FROM eod_prices p
         JOIN instrument_identifiers ii ON ii.instrument_id = p.instrument_id
        WHERE ii.value = $1`,
      [tickers[0]],
    )) as { n: number }[];
    expect(priceRows[0].n).toBe(SEED.rowsEach);

    const coverage = (await rawQuery(
      `SELECT count(*)::int AS n FROM price_coverage c
         JOIN instrument_identifiers ii ON ii.instrument_id = c.instrument_id
        WHERE ii.value = $1`,
      [tickers[0]],
    )) as { n: number }[];
    expect(coverage[0].n).toBeGreaterThan(0);

    // The instrument was matched rather than duplicated: an archive re-imported
    // into the instance that produced it must not fork its own security master.
    const instruments = (await rawQuery(
      `SELECT count(*)::int AS n FROM instrument_identifiers WHERE value LIKE 'E2E%'`,
    )) as { n: number }[];
    expect(instruments[0].n).toBe(SEED.instruments);

    // The FX pair already existed on this instance, so the import matched it.
    // A match must leave the reference data alone and still carry what the file
    // added -- here the recorded lookup result.
    const fxRow = (await rawQuery(
      `SELECT i.asset_class, i.currency, i.name,
              (SELECT count(*)::int FROM provider_instrument_identifiers p
                WHERE p.instrument_id = i.id AND p.identifier_type = 'EODHD_EXCH_CODE') AS provider_ids,
              (SELECT count(*)::int FROM instrument_identifiers x
                WHERE x.instrument_id = i.id AND x.identifier_type = 'FX_PAIR') AS fx_ids
         FROM instruments i
         JOIN instrument_identifiers ii ON ii.instrument_id = i.id
        WHERE ii.identifier_type = 'FX_PAIR' AND ii.value = $1`,
      [FX_PAIR],
    )) as { asset_class: string; currency: string; name: string; provider_ids: number; fx_ids: number }[];
    expect(fxRow).toHaveLength(1);
    expect(fxRow[0].asset_class).toBe("FX");
    expect(fxRow[0].currency).toBe("USD");
    expect(fxRow[0].provider_ids).toBe(1);
    expect(fxRow[0].fx_ids).toBe(1);

    // And the unclassified instrument came back as one row, not two.
    const unclassified = (await rawQuery(
      `SELECT count(*)::int AS n FROM instrument_identifiers WHERE value = $1`,
      [UNCLASSIFIED_TICKER],
    )) as { n: number }[];
    expect(unclassified[0].n).toBe(1);
  });

  test("refuses a file that is not a system archive", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");

    const notAnArchive = path.join(tmpdir(), "not-an-archive.json");
    await writeFile(
      notAnArchive,
      JSON.stringify({
        envelope: { format_version: 1, exported_at: "2026-07-30T00:00:00Z", kind: "USER" },
      }),
    );
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(notAnArchive);

    // Whether a file is a valid archive is answered at parse time, so this never
    // reaches a job.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "This is a user archive",
    );
    await expect(page.locator("[data-testid='start-archive-import']")).toHaveCount(0);
  });
});
