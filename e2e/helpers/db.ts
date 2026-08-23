// Postgres helper for E2E tests.
// Provides per-spec-file isolation by truncating test data and re-seeding.

import { Client } from "pg";
import * as fs from "fs";
import * as path from "path";
import { isRecordingSuite } from "./vcr";

const DATABASE_URL =
  process.env.E2E_DATABASE_URL ??
  "postgres://portfoliodb:portfoliodb@localhost:5434/portfoliodb";

let client: Client | null = null;

async function getClient(): Promise<Client> {
  if (!client) {
    client = new Client(DATABASE_URL);
    await client.connect();
  }
  return client;
}

// Every name of a security, at either grain, as a subquery a fixture can join.
//
// An identifier is stored against what its type names: an ISIN against the
// security, a ticker against one of its currency lines. Production code knows
// which grain it means before it asks, so it asks one table. A fixture checking
// "what is this instrument called" has no grain to state and wants both, which
// is the same flattening InstrumentRow.AllIdentifiers performs in Go.
export const INSTRUMENT_NAMES = `(
  SELECT instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before
  FROM instrument_identifiers
  UNION ALL
  SELECT l.instrument_id, li.identifier_type, li.domain, li.value, li.canonical, li.valid_from, li.valid_before
  FROM instrument_listing_identifiers li
  JOIN instrument_listings l ON l.id = li.listing_id
)`;

// Every provider identifier of a security, at either grain, for the same reason.
export const PROVIDER_INSTRUMENT_NAMES = `(
  SELECT instrument_id, provider, identifier_type, domain, value
  FROM provider_instrument_identifiers
  UNION ALL
  SELECT l.instrument_id, pli.provider, pli.identifier_type, pli.domain, pli.value
  FROM provider_listing_identifiers pli
  JOIN instrument_listings l ON l.id = pli.listing_id
)`;

export async function closeDB(): Promise<void> {
  if (client) {
    await client.end();
    client = null;
  }
}

// Remove all user-created test data. Preserves migration-seeded data
// (exchanges, currency/FX instruments, plugin_config).
export async function resetData(): Promise<void> {
  const c = await getClient();
  // Truncate user-scoped tables first (txs references instruments via FK).
  await c.query(`
    TRUNCATE
      holding_declarations,
      identification_errors,
      validation_errors,
      ingestion_jobs,
      txs,
      portfolio_filters,
      portfolios,
      users
    CASCADE
  `);
  // Delete non-seed instruments (CASCADE removes identifiers, prices, etc).
  // asset_class IS NULL covers broker-description-only instruments created
  // when identification fails (EnsureInstrument stores NULL asset_class).
  await c.query(
    `DELETE FROM instruments WHERE asset_class IS NULL OR asset_class NOT IN ('CASH', 'FX')`
  );
}

// Execute a SQL fixture file by name (relative to e2e/fixtures/).
//
// Postings written by a fixture are then put on the line of their security, which
// every fixture security has exactly one of. A posting names a security and the
// currency line within it, and a holding is per line -- so a fixture leaving the
// line unset would produce holdings that report unpriced, which is not what any of
// these fixtures is about. Doing it here rather than in each INSERT keeps the
// fixtures readable and stops a new one forgetting. A security with more than one
// line is left alone: which line its postings are on is then a real question, and
// a fixture about that states it itself.
export async function seedFixture(filename: string): Promise<void> {
  const c = await getClient();
  const sql = fs.readFileSync(
    path.resolve(__dirname, "../fixtures", filename),
    "utf-8"
  );
  await c.query(sql);
  await c.query(`
    UPDATE txs t
    SET listing_id = l.id
    FROM instrument_listings l
    WHERE l.instrument_id = t.instrument_id
      AND l.currency IS NOT NULL
      AND t.listing_id IS NULL
      AND NOT EXISTS (SELECT 1 FROM instrument_listings o
                      WHERE o.instrument_id = t.instrument_id AND o.id <> l.id)
  `);
}

// Seed plugin config.
//
// suite is the cassette this spec file records under, and is what decides
// whether the plugins get real API keys or the "REDACTED" placeholders their
// cassettes were sanitized to. Omit it for a spec that loads no cassette: with
// none loaded the server refuses outbound HTTP outright, so the key is never
// used and a real one would only be a secret sitting in a database for no
// reason.
//
// It is per-suite rather than "is anything recording" because recording one
// suite must not disturb the others. E2EMatcher compares the whole URL, and
// EODHD and Massive carry their keys in it, so a replaying suite seeded with a
// live key asks for a URL its cassette cannot hold and every one of its
// interactions misses. The rate limits are per-suite for the same reason: only
// the suite actually calling a provider needs to be slowed to its quota.
export async function seedPluginConfig(suite?: string): Promise<void> {
  const c = await getClient();
  const recording = suite !== undefined && isRecordingSuite(suite);

  const openaiKey = recording ? process.env.OPENAI_API_KEY ?? "" : "REDACTED";
  const openfigiKey = recording
    ? process.env.OPENFIGI_API_KEY ?? ""
    : "REDACTED";
  const eodhdKey = recording ? process.env.EODHD_API_KEY ?? "" : "REDACTED";
  const massiveKey = recording
    ? process.env.MASSIVE_API_KEY ?? ""
    : "REDACTED";

  // Rate limits only apply in record mode (real APIs).
  const eodhdCallsPerMin = recording ? 20 : null;
  const massiveCallsPerMin = recording ? 5 : null;

  await c.query(
    `INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
     VALUES
       ('openai', 'candidate', true, 1, $1::jsonb),
       ('cash', 'candidate', true, 2, '{}'::jsonb)
     ON CONFLICT (plugin_id, category) DO UPDATE SET
       enabled = EXCLUDED.enabled,
       precedence = EXCLUDED.precedence,
       config = EXCLUDED.config`,
    [
      JSON.stringify({
        openai_api_key: openaiKey,
        openai_model: "gpt-4o-mini",
        openai_base_url: "https://api.openai.com",
      }),
    ]
  );

  await c.query(
    `INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
     VALUES
       ('openfigi', 'identifier', true, 1, $1::jsonb),
       ('cash', 'identifier', true, 2, '{}'::jsonb),
       ('eodhd', 'identifier', true, 3, $2::jsonb),
       ('massive', 'identifier', true, 4, $3::jsonb)
     ON CONFLICT (plugin_id, category) DO UPDATE SET
       enabled = EXCLUDED.enabled,
       precedence = EXCLUDED.precedence,
       config = EXCLUDED.config`,
    [
      JSON.stringify({
        openfigi_api_key: openfigiKey,
        openfigi_base_url: "https://api.openfigi.com",
      }),
      JSON.stringify({
        eodhd_api_key: eodhdKey,
        eodhd_base_url: "https://eodhd.com",
        ...(eodhdCallsPerMin && { eodhd_calls_per_min: eodhdCallsPerMin }),
      }),
      JSON.stringify({
        massive_api_key: massiveKey,
        massive_base_url: "https://api.massive.com",
        ...(massiveCallsPerMin && {
          massive_calls_per_min: massiveCallsPerMin,
        }),
      }),
    ]
  );

  await c.query(
    `INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
     VALUES
       ('massive', 'price', true, 1, $1::jsonb)
     ON CONFLICT (plugin_id, category) DO UPDATE SET
       enabled = EXCLUDED.enabled,
       precedence = EXCLUDED.precedence,
       config = EXCLUDED.config`,
    [
      JSON.stringify({
        massive_api_key: massiveKey,
        massive_base_url: "https://api.massive.com",
        ...(massiveCallsPerMin && {
          massive_calls_per_min: massiveCallsPerMin,
        }),
      }),
    ]
  );

  await c.query(
    `INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
     VALUES
       ('eodhd', 'corporate_event', true, 1, $1::jsonb)
     ON CONFLICT (plugin_id, category) DO UPDATE SET
       enabled = EXCLUDED.enabled,
       precedence = EXCLUDED.precedence,
       config = EXCLUDED.config`,
    [
      JSON.stringify({
        eodhd_api_key: eodhdKey,
        eodhd_base_url: "https://eodhd.com",
        ...(eodhdCallsPerMin && { eodhd_calls_per_min: eodhdCallsPerMin }),
      }),
    ]
  );
}

// Override the Massive price plugin API key with an invalid value so that
// price fetch attempts return 403 (permanent error) and create fetch blocks.
// Only needed in record mode; in replay mode the VCR cassette replays the
// recorded 403 responses regardless of the configured key.
export async function corruptMassivePriceKey(): Promise<void> {
  const c = await getClient();
  await c.query(`
    UPDATE plugin_config
    SET config = jsonb_set(config, '{massive_api_key}', '"INVALID_E2E_KEY"')
    WHERE plugin_id = 'massive' AND category = 'price'
  `);
}

// Run a raw SQL query. Used for debugging in tests.
export async function rawQuery(sql: string, params?: unknown[]): Promise<unknown[]> {
  const c = await getClient();
  const res = await c.query(sql, params);
  return res.rows;
}

// Convenience: reset and seed the base data (users, portfolio, plugin config)
// that all tests need. Pass the cassette name a spec loads, so that recording it
// seeds real API keys and recording a different one does not -- see
// seedPluginConfig.
export async function resetAndSeedBase(suite?: string): Promise<void> {
  await resetData();
  await seedFixture("seed.sql");
  await seedPluginConfig(suite);
}

// Query instrument details by identifier. Returns null if not found.
export async function queryInstrumentByIdentifier(
  identifierType: string,
  identifierValue: string,
): Promise<{
  id: string;
  asset_class: string | null;
  strike: number | null;
  underlying_listing_id: string | null;
  underlying_id: string | null;
  identifiers: Array<{ type: string; value: string }>;
} | null> {
  const c = await getClient();
  // The stored column is the line the contract delivers; the security above it
  // is derived, for assertions that do not care which line.
  const instRes = await c.query(
    `SELECT i.id, i.asset_class, i.strike, i.underlying_listing_id, ul.instrument_id AS underlying_id
     FROM instruments i
     LEFT JOIN instrument_listings ul ON ul.id = i.underlying_listing_id
     JOIN ${INSTRUMENT_NAMES} ii ON ii.instrument_id = i.id
     WHERE ii.identifier_type = $1 AND ii.value = $2
     LIMIT 1`,
    [identifierType, identifierValue],
  );
  if (instRes.rows.length === 0) return null;
  const row = instRes.rows[0] as Record<string, unknown>;
  const idRes = await c.query(
    `SELECT identifier_type AS type, value FROM ${INSTRUMENT_NAMES} ii WHERE ii.instrument_id = $1`,
    [row.id],
  );
  return {
    id: row.id as string,
    asset_class: row.asset_class as string | null,
    strike: row.strike != null ? Number(row.strike) : null,
    underlying_listing_id: row.underlying_listing_id as string | null,
    underlying_id: row.underlying_id as string | null,
    identifiers: idRes.rows as Array<{ type: string; value: string }>,
  };
}
