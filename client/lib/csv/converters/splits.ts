/**
 * Shared types for broker split extractors. Each broker exposes an
 * extractor that scans a transaction file and returns SPLIT events for
 * submission via ImportCorporateEvents.
 *
 * Splits never travel through the user-facing tx upload path; they are
 * harvested separately by an admin via the Corporate Events admin page.
 */

import type { ParseError } from "@/lib/csv/standard";

export interface SplitIdentifier {
  /** Identifier type name as it appears in IdentifierType (e.g. "ISIN", "MIC_TICKER"). */
  type: string;
  value: string;
  /** Optional domain (e.g. MIC for MIC_TICKER). */
  domain?: string;
}

export interface ExtractedSplit {
  /** YYYY-MM-DD ex date. */
  exDate: string;
  /** Broker account string from the source row, when available. */
  account?: string;
  /** Best-effort identifier hint for ImportCorporateEventRow. */
  identifier: SplitIdentifier;
  /** Human-readable instrument description for the preview UI. */
  instrumentDescription: string;
  /** Decimal string. Populated when the broker file carries the ratio (IBKR). */
  splitFrom?: string;
  splitTo?: string;
  /** Schwab-only: signed quantity delta from the SPLIT row, used for prefill. */
  deltaShares?: string;
  /** 1-based source row number for surfacing parse errors. */
  sourceRow: number;
}

export interface SplitParseResult {
  splits: ExtractedSplit[];
  errors: ParseError[];
}

/**
 * Dedupe a list of extracted splits by (identifier value, ex date). When
 * the same split appears in multiple accounts the first occurrence wins;
 * its account field is cleared so the UI does not show a misleading
 * single-account scope.
 */
export function dedupeSplits(splits: ExtractedSplit[]): ExtractedSplit[] {
  const seen = new Map<string, ExtractedSplit>();
  for (const s of splits) {
    const key = `${s.identifier.type}:${s.identifier.value}|${s.exDate}`;
    const existing = seen.get(key);
    if (!existing) {
      seen.set(key, { ...s });
      continue;
    }
    // Collision: clear account to indicate cross-account span and sum
    // delta shares so prefill can use the total position change.
    existing.account = undefined;
    if (s.deltaShares != null) {
      const a = parseFloat(existing.deltaShares ?? "0");
      const b = parseFloat(s.deltaShares);
      if (Number.isFinite(a) && Number.isFinite(b)) {
        existing.deltaShares = String(a + b);
      }
    }
  }
  return Array.from(seen.values());
}
