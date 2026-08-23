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
import {
  resetAndSeedBase,
  closeDB,
  rawQuery,
  INSTRUMENT_NAMES,
  PROVIDER_INSTRUMENT_NAMES,
} from "../helpers/db";
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

// The curated state: an index series, a provider deliberately stopped, and a
// corporate event an admin has already ruled on. None of these can be
// reconstructed by the importing instance, and none of them is written by any
// path this test could drive through the UI.
const INFLATION_MONTH = "2024-01-01";
const INFLATION_VALUE = "131.5";
const BLOCKED_PLUGIN = "eodhd";
const BLOCK_REASON = "404 from provider";

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
  // A plugin config row this instance bootstrapped, flipped so that the file
  // and the database disagree until the import lands.
  let pluginRow: { plugin_id: string; enabled: boolean };

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
    const seed = writeGeneratedArchive({ ...SEED, filename: "roundtrip-seed.json" });
    tickers = seed.tickers;
    const job = await importSystemArchiveAndWait(adminSessionId, readArchive(seed.path));
    expect(job.status).toBe(JobStatus.SUCCESS);

    // An instrument identification has not classified yet. Created directly
    // because no ingest path reaches this state without calling a plugin.
    await rawQuery(
      `WITH i AS (INSERT INTO instruments (currency) VALUES ('USD') RETURNING id),
            l AS (INSERT INTO instrument_listings (instrument_id, currency)
                  SELECT id, 'USD' FROM i RETURNING id)
       INSERT INTO instrument_listing_identifiers (listing_id, identifier_type, domain, value, canonical)
       SELECT id, 'MIC_TICKER', 'XNAS', $1, true FROM l`,
      [UNCLASSIFIED_TICKER],
    );
    // The recorded output of a lookup, hung on reference data the importing
    // instance already has.
    await rawQuery(
      `INSERT INTO provider_listing_identifiers (listing_id, provider, identifier_type, value)
       SELECT l.id, 'eodhd', 'EODHD_EXCH_CODE', 'FOREX'
         FROM instrument_identifiers ii
         JOIN instrument_listings l ON l.instrument_id = ii.instrument_id
        WHERE ii.identifier_type = 'FX_PAIR' AND ii.value = $1
       ON CONFLICT DO NOTHING`,
      [FX_PAIR],
    );

    await rawQuery(
      `INSERT INTO inflation_indices (currency, month, index_value, base_year, data_provider)
       VALUES ('GBP', $1, $2, 2015, 'ons')`,
      [INFLATION_MONTH, INFLATION_VALUE],
    );
    // A price block is on one currency line, so it hangs off the listing and
    // travels with that line's currency.
    await rawQuery(
      `INSERT INTO price_fetch_blocks (listing_id, plugin_id, reason)
       SELECT l.id, $1, $2
         FROM ${INSTRUMENT_NAMES} ii
         JOIN instrument_listings l ON l.instrument_id = ii.instrument_id AND l.currency IS NOT NULL
        WHERE ii.value = $3`,
      [BLOCKED_PLUGIN, BLOCK_REASON, tickers[0]],
    );
    // Resolved, because the flag is the only trace that a person looked at this
    // and decided.
    await rawQuery(
      `INSERT INTO unhandled_corporate_events (instrument_id, event_type, ex_date, detail, resolved)
       SELECT ii.instrument_id, 'REVERSE_SPLIT', '2025-04-11', '1:10 reverse split', true
         FROM ${INSTRUMENT_NAMES} ii WHERE ii.value = $1`,
      [tickers[0]],
    );

    const rows = (await rawQuery(
      `SELECT plugin_id, enabled FROM plugin_config WHERE category = 'price' ORDER BY precedence DESC LIMIT 1`,
    )) as { plugin_id: string; enabled: boolean }[];
    pluginRow = rows[0];
    await rawQuery(`UPDATE plugin_config SET enabled = $1 WHERE category = 'price' AND plugin_id = $2`, [
      !pluginRow.enabled,
      pluginRow.plugin_id,
    ]);
  });

  test("exports a file and imports it back, preserving the data", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");
    await expect(page.getByRole("heading", { name: "Archive" })).toBeVisible();

    // Plugin config is the one part left unticked: it carries live API keys, so
    // including it is a deliberate choice. Every other part is on by default.
    const pluginConfig = page.getByLabel(/Plugin config/);
    await expect(pluginConfig).toBeEnabled();
    await expect(pluginConfig).not.toBeChecked();
    await pluginConfig.check();

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
    expect(doc.envelope.format_version).toBe(2);
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

    // The curated state: a human judgement or a paid fetch behind every one of
    // these, and nothing on the importing instance that could rebuild them.
    expect(doc.inflation_indices.groups[0].rows[0]).toMatchObject({
      month: INFLATION_MONTH,
      index_value: INFLATION_VALUE,
    });
    expect(doc.fetch_blocks.groups[0].blocks[0]).toMatchObject({
      category: "PRICE",
      plugin_id: BLOCKED_PLUGIN,
      reason: BLOCK_REASON,
    });
    expect(doc.unhandled_events.groups[0].events[0]).toMatchObject({
      event_type: "REVERSE_SPLIT",
      resolved: true,
    });
    const exportedPlugin = doc.plugin_config.configs.find(
      (c: { plugin_id: string; category: string }) =>
        c.plugin_id === pluginRow.plugin_id && c.category === "PRICE",
    );
    expect(exportedPlugin.enabled).toBe(!pluginRow.enabled);

    // Wipe the rows and re-import the file, so what lands afterwards came from
    // the archive rather than from what was already there.
    await rawQuery("DELETE FROM eod_prices");
    await rawQuery("DELETE FROM price_coverage");
    await rawQuery("DELETE FROM inflation_indices");
    await rawQuery("DELETE FROM price_fetch_blocks");
    await rawQuery("DELETE FROM unhandled_corporate_events");
    await rawQuery(`UPDATE plugin_config SET enabled = $1 WHERE category = 'price' AND plugin_id = $2`, [
      pluginRow.enabled,
      pluginRow.plugin_id,
    ]);

    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(exported);
    await expect(page.locator("[data-testid='archive-import']")).toContainText("Carries");
    await page.locator("[data-testid='start-archive-import']").click();

    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts.getByText("Done")).toHaveCount(7, { timeout: TIMEOUT_SLOW });

    // What the page said happened, checked against what is stored.
    const priceRows = (await rawQuery(
      `SELECT count(*)::int AS n FROM eod_prices p
         JOIN instrument_listings l ON l.id = p.listing_id
         JOIN ${INSTRUMENT_NAMES} ii ON ii.instrument_id = l.instrument_id
        WHERE ii.value = $1`,
      [tickers[0]],
    )) as { n: number }[];
    expect(priceRows[0].n).toBe(SEED.rowsEach);

    const coverage = (await rawQuery(
      `SELECT count(*)::int AS n FROM price_coverage c
         JOIN instrument_listings l ON l.id = c.listing_id
         JOIN ${INSTRUMENT_NAMES} ii ON ii.instrument_id = l.instrument_id
        WHERE ii.value = $1`,
      [tickers[0]],
    )) as { n: number }[];
    expect(coverage[0].n).toBeGreaterThan(0);

    // The instrument was matched rather than duplicated: an archive re-imported
    // into the instance that produced it must not fork its own security master.
    const instruments = (await rawQuery(
      `SELECT count(*)::int AS n FROM ${INSTRUMENT_NAMES} ii WHERE ii.value LIKE 'E2E%'`,
    )) as { n: number }[];
    expect(instruments[0].n).toBe(SEED.instruments);

    // The FX pair already existed on this instance, so the import matched it.
    // A match must leave the reference data alone and still carry what the file
    // added -- here the recorded lookup result.
    const fxRow = (await rawQuery(
      `SELECT i.asset_class, i.currency, i.name,
              (SELECT count(*)::int FROM ${PROVIDER_INSTRUMENT_NAMES} p
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
      `SELECT count(*)::int AS n FROM ${INSTRUMENT_NAMES} ii WHERE ii.value = $1`,
      [UNCLASSIFIED_TICKER],
    )) as { n: number }[];
    expect(unclassified[0].n).toBe(1);

    // The curated state is back, judgements included. Each of these is a row
    // nothing on this instance could have reconstructed.
    const inflation = (await rawQuery(
      `SELECT index_value::text AS v, base_year FROM inflation_indices WHERE currency = 'GBP'`,
    )) as { v: string; base_year: number }[];
    expect(inflation).toHaveLength(1);
    expect(inflation[0].base_year).toBe(2015);

    // The block came back on the line it was on, which is what the file's
    // currency carries: restoring it onto the security would have lost which.
    const blocks = (await rawQuery(
      `SELECT b.plugin_id, b.reason, l.currency
         FROM price_fetch_blocks b
         JOIN instrument_listings l ON l.id = b.listing_id`,
    )) as { plugin_id: string; reason: string; currency: string }[];
    expect(blocks).toEqual([
      { plugin_id: BLOCKED_PLUGIN, reason: BLOCK_REASON, currency: "USD" },
    ]);

    const events = (await rawQuery(
      `SELECT event_type, resolved FROM unhandled_corporate_events`,
    )) as { event_type: string; resolved: boolean }[];
    expect(events).toEqual([{ event_type: "REVERSE_SPLIT", resolved: true }]);

    const plugin = (await rawQuery(
      `SELECT enabled FROM plugin_config WHERE category = 'price' AND plugin_id = $1`,
      [pluginRow.plugin_id],
    )) as { enabled: boolean }[];
    expect(plugin[0].enabled).toBe(!pluginRow.enabled);
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
