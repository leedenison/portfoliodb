/** Recipe registry and the generic interpreter that renders a recipe's request. */

import type { Broker } from "@/gen/api/v1/api_pb";
import { formatDate, lastCoveredDay } from "../lib/dates";
import { fidelityUk } from "./fidelity-uk";
import type { BrokerRecipe, DateWindow, ExportRequest } from "./types";

export type { BrokerRecipe, DateWindow, ExportRequest } from "./types";

const RECIPES: BrokerRecipe[] = [fidelityUk];

export function listRecipes(): BrokerRecipe[] {
  return [...RECIPES];
}

export function getRecipe(id: string): BrokerRecipe | undefined {
  return RECIPES.find((r) => r.id === id);
}

export function getRecipeForBroker(broker: Broker): BrokerRecipe | undefined {
  return RECIPES.find((r) => r.broker === broker);
}

/** The ingestion source string for a recipe, e.g. "Fidelity:web:fidelity-csv". */
export function sourceFor(recipe: BrokerRecipe): string {
  return `${recipe.sourcePrefix}:web:${recipe.sourceFormatId}`;
}

/**
 * Substitutes the window into a recipe's export request.
 *
 * Dates are inserted verbatim rather than percent-encoded, because the site's own
 * request carries literal slashes in the query string.
 */
export function renderExport(recipe: BrokerRecipe, window: DateWindow): ExportRequest {
  const from = formatDate(window.from, recipe.dateFormat);
  // Broker date parameters are inclusive; our upper bound is exclusive.
  const to = formatDate(lastCoveredDay(window.before), recipe.dateFormat);
  const fill = (s: string) => s.replaceAll("{{from}}", from).replaceAll("{{to}}", to);
  return {
    ...recipe.export,
    url: fill(recipe.export.url),
    ...(recipe.export.body ? { body: fill(recipe.export.body) } : {}),
  };
}
