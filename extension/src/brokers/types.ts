/**
 * Broker recipes.
 *
 * A recipe holds everything about how one broker's site is driven: which origin,
 * which request, how dates are spelled. Repairing a broker that changed its site
 * should be an edit to a recipe, not to logic, so the interpreter that executes a
 * recipe contains no broker knowledge.
 *
 * Parsing the captured payload is code rather than data, because a payload format
 * is not expressible as a template. The split is deliberate: a broker moving a URL
 * or renaming a parameter is a data change; a broker changing its payload
 * structure is a code change.
 */

import type { Broker } from "@/gen/api/v1/api_pb";
import type { StandardParseResult } from "@/lib/csv/standard";

/** A request to replay against the broker, with {{from}} and {{to}} substituted. */
export interface ExportRequest {
  method: "GET" | "POST";
  /**
   * URL template. {{from}} and {{to}} are replaced with dates formatted using the
   * recipe's dateFormat, and are NOT percent-encoded: Fidelity's own request
   * sends "fromDate=02/07/2026" with literal slashes, and encoding them changes
   * the request the server sees.
   */
  url: string;
  headers?: Record<string, string>;
  /** Body template for POST, with the same substitutions. */
  body?: string;
}

export interface BrokerRecipe {
  /** Stable recipe id, e.g. "fidelity-uk". */
  id: string;
  broker: Broker;
  /**
   * Broker component of the ingestion source string, matching what the web
   * client sends. Held on the recipe rather than in a broker-keyed table
   * elsewhere, so everything that composes a source string for a broker lives in
   * one place.
   */
  sourcePrefix: string;
  /**
   * Format component of the ingestion source string.
   *
   * Pinned to whatever the web client already sends for this broker, even when
   * the extension reads a different payload. Source is the instrument-resolution
   * cache key and the domain of BROKER_DESCRIPTION identifiers, so a new value
   * would resolve descriptions afresh -- forking existing instruments and paying
   * for identification calls that have already been made.
   */
  sourceFormatId: string;
  /** Match patterns for the host permissions this recipe needs. */
  origins: string[];
  /** Opened when no tab on the broker's site is available to run the export in. */
  homeUrl: string;
  /** Decides which calendar day counts as "yesterday" when sizing the window. */
  timeZone: string;
  /** Token pattern for {{from}} and {{to}}; see formatDate. */
  dateFormat: string;
  export: ExportRequest;
  /**
   * True when the broker restates historical rows into current share terms --
   * showing post-split quantities on pre-split trades. The extension reads the
   * broker's live web UI, so this is the one import path where it can happen.
   *
   * Defaults to false, the as-traded assumption: a broker log line accounts
   * only for events prior to the trade. Setting it makes the upload declare
   * that its quantities are denominated as of the run, so the server does not
   * apply splits that the broker has already applied. Getting it wrong in
   * either direction scales historical quantities by the split factor.
   * See docs/spec/bitemporality.md.
   */
  restatesHistoricalQuantities?: boolean;
  /** Turns the captured payload into standard transactions. */
  convert: (payload: string, options?: Record<string, unknown>) => StandardParseResult;
}

export interface DateWindow {
  from: Date;
  to: Date;
}
