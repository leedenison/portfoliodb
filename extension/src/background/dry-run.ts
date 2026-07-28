/**
 * Captures and converts an export without uploading anything.
 *
 * This is how a recipe is developed and repaired against the live site: it
 * exercises every step a sync does except the one that writes.
 */

import { getRecipe } from "../brokers";
import { loadConfig } from "../config";
import { formatDate, parseSlashDate } from "../lib/dates";
import type { DryRunRequest, DryRunResult } from "../lib/messages";
import { captureExport } from "./export";

/** Parses "yyyy-MM-dd" from a date input to local midnight. */
function parseInputDate(value: string): Date | null {
  const m = value.match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (!m) return null;
  return parseSlashDate(`${m[3]}/${m[2]}/${m[1]}`);
}

/**
 * Counts rows and the transaction types the converter rejected. The type name is
 * pulled from the parse error rather than the payload so the two cannot disagree
 * about which rows were dropped.
 */
function summariseDropped(errors: { field: string; message: string }[]): string[] {
  const types = new Set<string>();
  for (const e of errors) {
    const m = e.field === "type" ? e.message.match(/Unknown transaction type: (.+)$/) : null;
    if (m?.[1]) types.add(m[1]);
  }
  return [...types].sort();
}

export async function dryRun(req: DryRunRequest): Promise<DryRunResult> {
  const recipe = getRecipe(req.recipeId);
  if (!recipe) return { ok: false, error: `no recipe named ${req.recipeId}` };

  const from = parseInputDate(req.from);
  const to = parseInputDate(req.to);
  if (!from || !to) return { ok: false, error: "enter both dates" };
  if (from > to) return { ok: false, error: "from is after to" };

  const requested = {
    from: formatDate(from, recipe.dateFormat),
    to: formatDate(to, recipe.dateFormat),
  };

  try {
    const captured = await captureExport(recipe, { from, to });
    const { currency } = await loadConfig();
    const parsed = recipe.convert(captured.body, { currency });

    let rowCount: number | undefined;
    try {
      const rows: unknown = JSON.parse(captured.body);
      if (Array.isArray(rows)) rowCount = rows.length;
    } catch {
      // Not JSON; the converter's own error will explain why.
    }

    return {
      ok: parsed.txs.length > 0,
      requested,
      ...(rowCount !== undefined ? { rowCount } : {}),
      txCount: parsed.txs.length,
      droppedRows: parsed.errors.length,
      droppedTypes: summariseDropped(parsed.errors),
      ...(parsed.txs.length === 0
        ? {
            error: parsed.errors[0]?.message ?? "the export contained no transactions",
            preview: captured.body.slice(0, 300),
          }
        : {}),
    };
  } catch (e) {
    return { ok: false, requested, error: e instanceof Error ? e.message : String(e) };
  }
}
