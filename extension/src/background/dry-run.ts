/**
 * Captures and converts an export without uploading anything.
 *
 * This is how a recipe is developed and repaired against the live site: it
 * exercises every step a sync does except the one that writes.
 */

import { getRecipe } from "../brokers";
import { loadConfig } from "../config";
import { formatDate, parseIsoDate, startOfNextDay } from "../lib/dates";
import type { DryRunRequest, DryRunResult } from "../lib/messages";
import { droppedTypes } from "../lib/dropped";
import { captureExport } from "./export";

export async function dryRun(req: DryRunRequest): Promise<DryRunResult> {
  const recipe = getRecipe(req.recipeId);
  if (!recipe) return { ok: false, error: `no recipe named ${req.recipeId}` };

  const from = parseIsoDate(req.from);
  const to = parseIsoDate(req.to);
  if (!from || !to) return { ok: false, error: "enter both dates" };
  if (from > to) return { ok: false, error: "from is after to" };

  const requested = {
    from: formatDate(from, recipe.dateFormat),
    to: formatDate(to, recipe.dateFormat),
  };

  try {
    // The dry-run form takes the inclusive last day a person would type.
    const captured = await captureExport(recipe, { from, before: startOfNextDay(to) });
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
      ok: parsed.postings.length > 0,
      requested,
      ...(rowCount !== undefined ? { rowCount } : {}),
      txCount: parsed.postings.length,
      droppedCount: parsed.errors.length,
      droppedTypes: droppedTypes(parsed.errors),
      ...(parsed.postings.length === 0
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
