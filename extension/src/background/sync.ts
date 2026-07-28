/**
 * One attended sync: work out the missing period, fetch it, convert it, upload
 * it, and wait for the ingestion job to finish.
 *
 * Every exit records a run-log entry, including the failures -- a sync that
 * silently did nothing is the hardest kind to diagnose.
 */

import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Broker, JobStatus } from "@/gen/api/v1/api_pb";
import { getRecipeForBroker, sourceFor } from "../brokers";
import type { BrokerRecipe } from "../brokers/types";
import { loadConfig } from "../config";
import { getJob, listTxs, upsertTxs } from "../lib/api";
import { formatDate } from "../lib/dates";
import { droppedTypes } from "../lib/dropped";
import { recordRun, type RunLogEntry } from "../lib/run-log";
import { getSessionId } from "../lib/session";
import { computeWindow } from "../lib/window";
import { captureExport } from "./export";

/** Ingestion is asynchronous; a bulk upload of a year takes a little while. */
const JOB_POLL_INTERVAL_MS = 1_500;
const JOB_TIMEOUT_MS = 120_000;

/** Source prefix per broker, matching what the web client sends. */
const SOURCE_PREFIX: Partial<Record<Broker, string>> = {
  [Broker.FIDELITY]: "Fidelity",
  [Broker.IBKR]: "IBKR",
};

export interface SyncResult {
  entry: RunLogEntry;
}

async function finish(entry: RunLogEntry): Promise<SyncResult> {
  await recordRun(entry);
  return { entry };
}

/** Most recent transaction already held for a broker, or null if there are none. */
async function latestTx(origin: string, sessionId: string, broker: Broker): Promise<Date | null> {
  const res = await listTxs(origin, sessionId, { broker, descending: true, pageSize: 1 });
  const ts = res.txs[0]?.tx?.timestamp;
  return ts ? timestampDate(ts) : null;
}

async function awaitJob(
  origin: string,
  sessionId: string,
  jobId: string,
  sleep: (ms: number) => Promise<void>
): Promise<{ ok: boolean; errors: string[] }> {
  const deadline = Date.now() + JOB_TIMEOUT_MS;
  for (;;) {
    const job = await getJob(origin, sessionId, jobId);
    if (job.status === JobStatus.SUCCESS) return { ok: true, errors: [] };
    if (job.status === JobStatus.FAILED) {
      return {
        ok: false,
        errors: [
          ...job.validationErrors.map((e) => `row ${e.rowIndex} ${e.field}: ${e.message}`),
          ...job.identificationErrors.map((e) => e.message),
        ],
      };
    }
    if (Date.now() > deadline) {
      return { ok: false, errors: ["timed out waiting for the ingestion job"] };
    }
    await sleep(JOB_POLL_INTERVAL_MS);
  }
}

export interface SyncOptions {
  broker: Broker;
  /** Injected for tests. */
  now?: Date;
  sleep?: (ms: number) => Promise<void>;
}

export async function sync(opts: SyncOptions): Promise<SyncResult> {
  const now = opts.now ?? new Date();
  const sleep = opts.sleep ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  const startedAt = now.toISOString();

  const recipe: BrokerRecipe | undefined = getRecipeForBroker(opts.broker);
  if (!recipe) {
    return finish({ startedAt, recipeId: "", status: "failed", error: "No recipe for this broker" });
  }
  const base: RunLogEntry = { startedAt, recipeId: recipe.id, status: "failed" };

  const config = await loadConfig();
  if (!config.portfoliodbOrigin) {
    return finish({ ...base, error: "Set the PortfolioDB origin in settings" });
  }
  const sessionId = await getSessionId();
  if (!sessionId) {
    return finish({ ...base, error: "Not connected to PortfolioDB. Connect first." });
  }

  let latest: Date | null;
  try {
    latest = await latestTx(config.portfoliodbOrigin, sessionId, opts.broker);
  } catch (e) {
    return finish({ ...base, error: `Could not read existing transactions: ${message(e)}` });
  }

  const win = computeWindow({
    latest,
    historyStartDate: config.historyStartDate,
    lookbackDays: config.lookbackDays,
    timeZone: config.timeZone,
    now,
  });
  const resumedFrom = latest ? latest.toISOString() : null;
  if (win.kind === "error") return finish({ ...base, resumedFrom, error: win.message });
  if (win.kind === "up-to-date") {
    return finish({ ...base, status: "up-to-date", resumedFrom });
  }

  const requested = {
    from: formatDate(win.from, recipe.dateFormat),
    to: formatDate(win.to, recipe.dateFormat),
  };
  const withWindow: RunLogEntry = { ...base, resumedFrom, window: requested };

  let payload: string;
  try {
    payload = (await captureExport(recipe, { from: win.from, to: win.to })).body;
  } catch (e) {
    return finish({ ...withWindow, error: `Export failed: ${message(e)}` });
  }

  const parsed = recipe.convert(payload, { currency: config.currency });
  const dropped = droppedTypes(parsed.errors);
  const counted: RunLogEntry = {
    ...withWindow,
    rowCount: rowCount(payload),
    txCount: parsed.txs.length,
    droppedRows: parsed.errors.length,
    ...(dropped.length > 0 ? { droppedTypes: dropped } : {}),
  };

  if (parsed.txs.length === 0) {
    // Refusing rather than uploading nothing: the server marks a bulk upload with
    // no storable rows successful without performing the replace, so an empty
    // upload would report success while deleting nothing.
    return finish({
      ...counted,
      error:
        parsed.errors[0]?.message ??
        "The export contained no transactions, so the period cannot be replaced",
    });
  }

  let jobId: string;
  try {
    const res = await upsertTxs(config.portfoliodbOrigin, sessionId, {
      broker: opts.broker,
      source: sourceFor(recipe, SOURCE_PREFIX[opts.broker] ?? "unknown"),
      // The window that was requested, not the range of the rows that came back:
      // that is what lets the replace delete transactions the broker cancelled.
      periodFrom: timestampFromDate(win.from),
      periodTo: timestampFromDate(win.to),
      txs: parsed.txs,
      filename: `${recipe.id}-${formatDate(win.to, "yyyy-MM-dd")}.json`,
    });
    jobId = res.jobId;
  } catch (e) {
    return finish({ ...counted, error: `Upload failed: ${message(e)}` });
  }

  const job = await awaitJob(config.portfoliodbOrigin, sessionId, jobId, sleep);
  if (!job.ok) {
    return finish({ ...counted, jobId, jobErrors: job.errors, error: "The ingestion job failed" });
  }
  // Dropped rows are not fatal, but they are not a clean run either: the replace
  // deleted whatever those rows previously stored.
  return finish({ ...counted, jobId, status: dropped.length > 0 || counted.droppedRows ? "warning" : "success" });
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function rowCount(payload: string): number | undefined {
  try {
    const rows: unknown = JSON.parse(payload);
    return Array.isArray(rows) ? rows.length : undefined;
  } catch {
    return undefined;
  }
}
