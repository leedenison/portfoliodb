// Postgres helper for E2E tests.
// Provides per-spec-file isolation by truncating test data and re-seeding.

import { Client } from "pg";
import * as fs from "fs";
import * as path from "path";
import { isRecording } from "./vcr";

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
export async function seedFixture(filename: string): Promise<void> {
  const c = await getClient();
  const sql = fs.readFileSync(
    path.resolve(__dirname, "../fixtures", filename),
    "utf-8"
  );
  await c.query(sql);
}

// Seed plugin config. When any suite is being recorded (VCR_MODE is non-empty)
// uses real API keys from env vars; in replay mode uses "REDACTED" placeholders.
export async function seedPluginConfig(): Promise<void> {
  const c = await getClient();
  const recording = isRecording();

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
       ('openai', 'description', true, 1, $1::jsonb),
       ('cash', 'description', true, 2, '{}'::jsonb)
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
// that all tests need.
export async function resetAndSeedBase(): Promise<void> {
  await resetData();
  await seedFixture("seed.sql");
  await seedPluginConfig();
}

// Query split-adjusted tx values for an instrument identified by
// (identifier_type, identifier_value). Returns rows ordered by tx date.
export async function queryTxSplitAdjustments(
  identifierType: string,
  identifierValue: string,
): Promise<
  Array<{
    quantity: number;
    split_adjusted_quantity: number;
    unit_price: number | null;
    split_adjusted_unit_price: number | null;
    timestamp: Date;
  }>
> {
  const c = await getClient();
  const res = await c.query(
    `SELECT t.quantity, t.split_adjusted_quantity,
            t.unit_price, t.split_adjusted_unit_price,
            t.timestamp
     FROM txs t
     JOIN instrument_identifiers ii ON ii.instrument_id = t.instrument_id
     WHERE ii.identifier_type = $1 AND ii.value = $2
     ORDER BY t.timestamp`,
    [identifierType, identifierValue],
  );
  return res.rows.map((r: Record<string, unknown>) => ({
    quantity: Number(r.quantity),
    split_adjusted_quantity: Number(r.split_adjusted_quantity),
    unit_price: r.unit_price != null ? Number(r.unit_price) : null,
    split_adjusted_unit_price:
      r.split_adjusted_unit_price != null
        ? Number(r.split_adjusted_unit_price)
        : null,
    timestamp: r.timestamp as Date,
  }));
}

// Query instrument details by identifier. Returns null if not found.
export async function queryInstrumentByIdentifier(
  identifierType: string,
  identifierValue: string,
): Promise<{
  id: string;
  asset_class: string | null;
  strike: number | null;
  underlying_id: string | null;
  identifiers: Array<{ type: string; value: string }>;
} | null> {
  const c = await getClient();
  const instRes = await c.query(
    `SELECT i.id, i.asset_class, i.strike, i.underlying_id
     FROM instruments i
     JOIN instrument_identifiers ii ON ii.instrument_id = i.id
     WHERE ii.identifier_type = $1 AND ii.value = $2
     LIMIT 1`,
    [identifierType, identifierValue],
  );
  if (instRes.rows.length === 0) return null;
  const row = instRes.rows[0] as Record<string, unknown>;
  const idRes = await c.query(
    `SELECT identifier_type AS type, value FROM instrument_identifiers WHERE instrument_id = $1`,
    [row.id],
  );
  return {
    id: row.id as string,
    asset_class: row.asset_class as string | null,
    strike: row.strike != null ? Number(row.strike) : null,
    underlying_id: row.underlying_id as string | null,
    identifiers: idRes.rows as Array<{ type: string; value: string }>,
  };
}
