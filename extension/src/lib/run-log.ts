/**
 * A record of what each sync did.
 *
 * This is what makes a run diagnosable after the fact, and it is where the
 * information needed to extend a converter's type map comes from: a run that
 * dropped rows names the broker transaction types it did not recognise.
 */

import type { ParseError } from "@/lib/csv/standard";

const STORAGE_KEY = "runLog";
const MAX_ENTRIES = 20;

export type RunStatus = "success" | "warning" | "failed" | "up-to-date";

export interface RunLogEntry {
  /** ISO timestamp of when the run started. */
  startedAt: string;
  recipeId: string;
  status: RunStatus;
  /** The window requested, in the broker's own date format. */
  window?: { from: string; to: string };
  /** The transaction the window was derived from, or null on a first run. */
  resumedFrom?: string | null;
  rowCount?: number;
  txCount?: number;
  droppedCount?: number;
  /**
   * Every row the converter refused, with its index in the payload and why.
   *
   * Kept in full rather than summarised: a row dropped for a reason other than an
   * unrecognised type -- a malformed date, say -- contributes nothing to
   * droppedTypes, and a bare count gives no way to find it. A replace deletes
   * whatever those rows previously stored, so the run has to be able to say
   * exactly what was lost.
   */
  droppedRows?: ParseError[];
  /** Distinct broker transaction types the converter did not recognise. */
  droppedTypes?: string[];
  jobId?: string;
  /** Validation and identification errors reported by the ingestion job. */
  jobErrors?: string[];
  error?: string;
}

export async function listRuns(): Promise<RunLogEntry[]> {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  const runs = stored[STORAGE_KEY];
  return Array.isArray(runs) ? (runs as RunLogEntry[]) : [];
}

/** Prepends an entry, keeping only the most recent MAX_ENTRIES. */
export async function recordRun(entry: RunLogEntry): Promise<void> {
  const runs = [entry, ...(await listRuns())].slice(0, MAX_ENTRIES);
  await chrome.storage.local.set({ [STORAGE_KEY]: runs });
}

export async function clearRuns(): Promise<void> {
  await chrome.storage.local.remove(STORAGE_KEY);
}
