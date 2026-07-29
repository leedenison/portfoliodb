/**
 * JSON serialization for corporate event (split) export/import.
 * Format: array of split objects with identifier context.
 */

import { timestampDate } from "@bufbuild/protobuf/wkt";
import { AssetClass } from "@/gen/api/v1/api_pb";
import type { ExportCorporateEventRow, SplitRow } from "@/gen/api/v1/api_pb";
import type { CorporateSplitImportRow } from "@/lib/portfolio-api";
import { assetClassToStr, assetClassFromStr } from "@/lib/asset-class";
import type { ParseError } from "@/lib/csv/standard";

interface SerializedSplit {
  identifier_type: string;
  identifier_value: string;
  identifier_domain?: string;
  asset_class?: string;
  ex_date: string;
  split_from: string;
  split_to: string;
  // Knowledge time as an ISO 8601 instant: when the exporting instance first
  // learned of the split. Absent in files written before it was recorded.
  first_known_at?: string;
}

/** Serialize split export rows to JSON string. */
export function splitsToJson(rows: ExportCorporateEventRow[]): string {
  const serialized: SerializedSplit[] = rows
    .filter((r) => r.event.case === "split")
    .map((r) => {
      const split = r.event.value as SplitRow;
      const obj: SerializedSplit = {
        identifier_type: r.identifierType,
        identifier_value: r.identifierValue,
        ex_date: split.exDate,
        split_from: split.splitFrom,
        split_to: split.splitTo,
      };
      if (r.identifierDomain) obj.identifier_domain = r.identifierDomain;
      const ac = assetClassToStr(r.assetClass);
      if (ac) obj.asset_class = ac;
      if (split.firstKnownAt) obj.first_known_at = timestampDate(split.firstKnownAt).toISOString();
      return obj;
    });
  return JSON.stringify(serialized, null, 2) + "\n";
}

export interface SplitParseResult {
  splits: CorporateSplitImportRow[];
  errors: ParseError[];
}

/** Parse splits JSON back into importable rows. */
export function parseSplitsJson(json: string): SplitParseResult {
  const errors: ParseError[] = [];
  let parsed: unknown;

  try {
    parsed = JSON.parse(json);
  } catch (e) {
    return {
      splits: [],
      errors: [{ rowIndex: 0, field: "file", message: `Invalid JSON: ${e instanceof Error ? e.message : String(e)}` }],
    };
  }

  if (!Array.isArray(parsed)) {
    return {
      splits: [],
      errors: [{ rowIndex: 0, field: "file", message: "Expected a JSON array" }],
    };
  }

  const splits: CorporateSplitImportRow[] = [];

  for (let i = 0; i < parsed.length; i++) {
    const item = parsed[i];
    if (typeof item !== "object" || item === null) {
      errors.push({ rowIndex: i, field: "item", message: "Expected an object" });
      continue;
    }
    const obj = item as Record<string, unknown>;

    const identifierType = String(obj.identifier_type ?? "");
    const identifierValue = String(obj.identifier_value ?? "");
    if (!identifierType) {
      errors.push({ rowIndex: i, field: "identifier_type", message: "Required" });
      continue;
    }
    if (!identifierValue) {
      errors.push({ rowIndex: i, field: "identifier_value", message: "Required" });
      continue;
    }

    const exDate = String(obj.ex_date ?? "");
    if (!exDate || !/^\d{4}-\d{2}-\d{2}$/.test(exDate)) {
      errors.push({ rowIndex: i, field: "ex_date", message: "Required, format YYYY-MM-DD" });
      continue;
    }

    const splitFrom = String(obj.split_from ?? "");
    const splitTo = String(obj.split_to ?? "");
    if (!splitFrom || !splitTo) {
      errors.push({ rowIndex: i, field: "split_from/split_to", message: "Both split_from and split_to are required" });
      continue;
    }
    const fromNum = parseFloat(splitFrom);
    const toNum = parseFloat(splitTo);
    if (!Number.isFinite(fromNum) || fromNum <= 0 || !Number.isFinite(toNum) || toNum <= 0) {
      errors.push({ rowIndex: i, field: "split_from/split_to", message: "Must be positive numbers" });
      continue;
    }

    const row: CorporateSplitImportRow = {
      identifierType,
      identifierValue,
      exDate,
      splitFrom,
      splitTo,
    };
    const domain = String(obj.identifier_domain ?? "");
    if (domain) row.identifierDomain = domain;
    const ac = assetClassFromStr(String(obj.asset_class ?? ""));
    if (ac !== AssetClass.UNSPECIFIED) row.assetClass = ac;

    // Knowledge time is optional: a file that omits it is imported with the
    // server's fallback rather than rejected. An unparseable value is an
    // error, since silently dropping it would restamp the split.
    if (obj.first_known_at != null && obj.first_known_at !== "") {
      const knownAt = new Date(String(obj.first_known_at));
      if (Number.isNaN(knownAt.getTime())) {
        errors.push({ rowIndex: i, field: "first_known_at", message: "Must be an ISO 8601 timestamp" });
        continue;
      }
      row.firstKnownAt = knownAt;
    }

    splits.push(row);
  }

  return { splits, errors };
}
