// E2E test: price-first instrument creation, then transaction-driven enrichment.
//
// Verifies the merge scenario where prices are imported for an unknown
// instrument (creating it with just the supplied identifier), then a
// transaction is uploaded whose broker description resolves to the same
// ticker. The ingestion flow enriches the instrument via identifier
// plugins (adding ISIN, OPENFIGI_*, etc.) and the prices remain
// associated with the now-enriched instrument.

import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { uploadCSVAndWait } from "../helpers/upload";
import { importPrices } from "../helpers/api";
import { Client } from "pg";

const DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  "postgres://portfoliodb:portfoliodb@localhost:5434/portfoliodb";

let db: Client;

test.beforeAll(async () => {
  await loadCassette("price-import-merge");
  await resetAndSeedBase();
  db = new Client(DATABASE_URL);
  await db.connect();
});

test.afterAll(async () => {
  await db.end();
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("price-first instrument merge", () => {
  let adminSessionId: string;
  let userSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
    userSessionId = await seedSession("user");
  });

  test("import prices for unknown instrument, then enrich via transaction", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSessionId);

    // Step 1: Import prices for an unknown instrument (TICKER AAPL, no
    // asset_class). Plugins are skipped; instrument created with just TICKER.
    const priceResp = await importPrices(adminSessionId, [
      {
        identifierType: "TICKER",
        identifierValue: "AAPL",
        priceDate: "2023-12-01",
        close: 190.50,
      },
      {
        identifierType: "TICKER",
        identifierValue: "AAPL",
        priceDate: "2023-12-04",
        close: 191.25,
      },
    ]);
    expect(priceResp.upsertedCount).toBe(2);
    expect(priceResp.errors).toHaveLength(0);

    // Step 2: Verify the instrument exists with only the TICKER identifier.
    const preEnrich = await db.query(
      `SELECT i.id, i.asset_class, i.name
       FROM instruments i
       JOIN instrument_identifiers ii ON ii.instrument_id = i.id
       WHERE ii.identifier_type = 'TICKER' AND ii.value = 'AAPL'`
    );
    expect(preEnrich.rows).toHaveLength(1);
    const instrumentId = preEnrich.rows[0].id;
    // asset_class should be empty (no plugins were called).
    expect(preEnrich.rows[0].asset_class).toBeNull();

    const preIds = await db.query(
      `SELECT identifier_type, value, canonical
       FROM instrument_identifiers
       WHERE instrument_id = $1
       ORDER BY identifier_type`,
      [instrumentId]
    );
    // Should have only the TICKER identifier.
    expect(preIds.rows).toHaveLength(1);
    expect(preIds.rows[0].identifier_type).toBe("TICKER");
    expect(preIds.rows[0].value).toBe("AAPL");
    expect(preIds.rows[0].canonical).toBe(true);

    // Verify prices are attached.
    const prePrices = await db.query(
      `SELECT COUNT(*) AS cnt FROM eod_prices WHERE instrument_id = $1`,
      [instrumentId]
    );
    expect(Number(prePrices.rows[0].cnt)).toBe(2);

    // Step 3: Upload a CSV transaction with ISIN hint for AAPL. This
    // triggers the full identification pipeline: description extraction
    // + identifier plugins enrich the instrument.
    await uploadCSVAndWait(page, browser, "single-aapl-stock.csv", {
      expectedTxCount: 1,
    });

    // Step 4: Verify the instrument was enriched. The ingestion flow's
    // resolveByHintsDBOnly should find the existing instrument by TICKER
    // (or ISIN), then EnsureInstrument merges in new identifiers.
    const postIds = await db.query(
      `SELECT identifier_type, value, canonical
       FROM instrument_identifiers
       WHERE instrument_id = $1
       ORDER BY identifier_type`,
      [instrumentId]
    );
    // Should now have additional identifiers beyond TICKER.
    expect(postIds.rows.length).toBeGreaterThan(1);

    // Check for expected identifier types.
    const idTypes = postIds.rows.map(
      (r: { identifier_type: string }) => r.identifier_type
    );
    expect(idTypes).toContain("TICKER");
    // The ISIN from the CSV should be present (or plugin-added identifiers).
    // At minimum, broker description should be added.
    expect(idTypes).toContain("BROKER_DESCRIPTION");

    // Step 5: Verify prices are still associated with the same instrument
    // (same instrument_id, not a different instrument).
    const postPrices = await db.query(
      `SELECT COUNT(*) AS cnt FROM eod_prices WHERE instrument_id = $1`,
      [instrumentId]
    );
    expect(Number(postPrices.rows[0].cnt)).toBe(2);

    // Verify instrument was enriched with asset_class from plugins.
    const postInst = await db.query(
      `SELECT asset_class, name FROM instruments WHERE id = $1`,
      [instrumentId]
    );
    expect(postInst.rows[0].asset_class).toBe("STOCK");
  });

  // In record mode, ensure all workers complete so the cassette captures
  // every HTTP interaction.
  if (process.env.VCR_MODE === "record") {
    test("wait for all workers to finish (record mode)", async ({
      browser,
    }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
