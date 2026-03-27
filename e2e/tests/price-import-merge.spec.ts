// E2E test: price-first instrument creation, then transaction reuse.
//
// Verifies the merge scenario where prices are imported for an unknown
// instrument (creating it with just the supplied identifier), then a
// transaction is uploaded whose broker description resolves to the same
// ticker. The ingestion flow finds the existing instrument via DB lookup
// (ResolveByHintsDBOnly matches TICKER "AAPL") and reuses it, so prices
// and transactions share the same instrument_id.
//
// Note: the CSV fixture omits the ISIN column so the ingestion takes
// Path B (description extraction). If ISIN were present, Path A would
// call plugins which return TICKER with a domain (e.g. "US"), and
// EnsureInstrument would create a separate instrument because the
// domain differs from the price-created identifier (empty domain).

import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { uploadCSVAndWait } from "../helpers/upload";
import { importPricesAndWait } from "../helpers/api";
import { JobStatus } from "../gen/api/v1/api_pb";
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

  test("import prices for unknown instrument, then reuse via transaction", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSessionId);

    // Step 1: Import prices for an unknown instrument (TICKER AAPL, no
    // asset_class). Plugins are skipped; instrument created with just TICKER.
    const priceResp = await importPricesAndWait(adminSessionId, [
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
    expect(priceResp.status).toBe(JobStatus.SUCCESS);
    expect(priceResp.validationErrors).toHaveLength(0);

    // Step 2: Verify the instrument exists with only the TICKER identifier.
    const preEnrich = await db.query(
      `SELECT i.id, i.asset_class, i.name
       FROM instruments i
       JOIN instrument_identifiers ii ON ii.instrument_id = i.id
       WHERE ii.identifier_type = 'TICKER' AND ii.value = 'AAPL'`
    );
    expect(preEnrich.rows).toHaveLength(1);
    const instrumentId = preEnrich.rows[0].id;
    expect(preEnrich.rows[0].asset_class).toBeNull();

    const preIds = await db.query(
      `SELECT identifier_type, value, canonical
       FROM instrument_identifiers
       WHERE instrument_id = $1
       ORDER BY identifier_type`,
      [instrumentId]
    );
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

    // Step 3: Upload a CSV transaction (no ISIN column). Description
    // extraction returns TICKER "AAPL" which matches the existing
    // instrument via ResolveByHintsDBOnly. The instrument is reused
    // without calling identifier plugins.
    await uploadCSVAndWait(page, browser, "single-aapl-stock.csv", {
      expectedTxCount: 1,
    });

    // Step 4: Verify the transaction was attached to the SAME instrument.
    // No new instrument should have been created for AAPL.
    const postInstruments = await db.query(
      `SELECT i.id
       FROM instruments i
       JOIN instrument_identifiers ii ON ii.instrument_id = i.id
       WHERE ii.identifier_type = 'TICKER' AND ii.value = 'AAPL'`
    );
    expect(postInstruments.rows).toHaveLength(1);
    expect(postInstruments.rows[0].id).toBe(instrumentId);

    // Verify the transaction is linked to the same instrument.
    const txs = await db.query(
      `SELECT COUNT(*) AS cnt FROM txs WHERE instrument_id = $1`,
      [instrumentId]
    );
    expect(Number(txs.rows[0].cnt)).toBe(1);

    // Step 5: Verify prices are still associated with the same instrument.
    const postPrices = await db.query(
      `SELECT COUNT(*) AS cnt FROM eod_prices WHERE instrument_id = $1`,
      [instrumentId]
    );
    expect(Number(postPrices.rows[0].cnt)).toBeGreaterThanOrEqual(2);
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
