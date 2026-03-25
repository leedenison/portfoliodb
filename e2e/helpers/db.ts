// Postgres helper for E2E tests.
// Provides per-spec-file isolation by truncating test data and re-seeding.

import { Client } from "pg";
import * as fs from "fs";
import * as path from "path";

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
  // Delete non-seed instruments (CASCADE removes identifiers, prices, etc).
  await c.query(
    `DELETE FROM instruments WHERE asset_class NOT IN ('CASH', 'FX')`
  );
  // Truncate user-scoped tables.
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

// Seed plugin config. In record mode (VCR_MODE=record) uses real API keys
// from env vars; in replay mode uses "REDACTED" placeholders.
export async function seedPluginConfig(): Promise<void> {
  const c = await getClient();
  const recording = process.env.VCR_MODE === "record";

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
}

// Convenience: reset and seed the base data (users, portfolio, plugin config)
// that all tests need.
export async function resetAndSeedBase(): Promise<void> {
  await resetData();
  await seedFixture("seed.sql");
  await seedPluginConfig();
}
