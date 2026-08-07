/**
 * JSON serialization for corporate event (split) export/import.
 *
 * The canonical import shape is an object with "events" -- split objects with
 * identifier context -- and an optional "coverage". A bare array of events is
 * still accepted, and is what the exporter writes until it has coverage to
 * serialize. See docs/spec/csv-format.md.
 */

import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ImportCorporateEventCoverageSchema } from "@/gen/api/v1/api_pb";
import { AssetClass } from "@/gen/type/v1/type_pb";
import type { ExportCorporateEventRow, ExportCoverage, ImportCorporateEventCoverage, SplitRow } from "@/gen/api/v1/api_pb";
import type { CorporateSplitImportRow } from "@/lib/portfolio-api";
import { assetClassToStr, assetClassFromStr } from "@/lib/asset-class";
import type { ParseError } from "@/lib/csv/standard";
import { expandCoverage, validateCoverage } from "@/lib/coverage";
import type { CoverageDecl, InstrumentRef } from "@/lib/coverage";

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

interface SerializedCoverage {
  identifier_type: string;
  identifier_value: string;
  identifier_domain?: string;
  from: string;
  before: string;
}

/**
 * Serialize split export rows to a JSON string in the canonical object form.
 * Coverage is written in the specific form, one entry per instrument.
 */
export function splitsToJson(rows: ExportCorporateEventRow[], coverage?: ExportCoverage[]): string {
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
  const out: { events: SerializedSplit[]; coverage?: SerializedCoverage[] } = { events: serialized };
  if (coverage && coverage.length > 0) {
    out.coverage = coverage.map((c) => {
      const obj: SerializedCoverage = {
        identifier_type: c.identifierType,
        identifier_value: c.identifierValue,
        from: c.from,
        before: c.before,
      };
      if (c.identifierDomain) obj.identifier_domain = c.identifierDomain;
      return obj;
    });
  }
  return JSON.stringify(out, null, 2) + "\n";
}

export interface SplitParseResult {
  splits: CorporateSplitImportRow[];
  /** One entry per instrument per span, with any global already expanded. */
  coverage: ImportCorporateEventCoverage[];
  errors: ParseError[];
}

/**
 * Read the "coverage" array. Entries carrying no identifier keys are global and
 * apply to every instrument in the file; see @/lib/coverage.
 */
function parseCoverageArray(raw: unknown, errors: ParseError[]): CoverageDecl[] {
  if (raw == null) return [];
  if (!Array.isArray(raw)) {
    errors.push({ rowIndex: 0, field: "coverage", message: "Expected an array" });
    return [];
  }

  const decls: CoverageDecl[] = [];
  for (let i = 0; i < raw.length; i++) {
    const item = raw[i];
    if (typeof item !== "object" || item === null) {
      errors.push({ rowIndex: i, field: "coverage", message: "Expected an object" });
      continue;
    }
    const obj = item as Record<string, unknown>;
    const decl: CoverageDecl = {
      from: String(obj.from ?? ""),
      before: String(obj.before ?? ""),
      rowIndex: i,
    };
    // Absent and empty are the same thing here: either way the declaration
    // names no instrument, which is what makes it global.
    const type = String(obj.identifier_type ?? "");
    const value = String(obj.identifier_value ?? "");
    const domain = String(obj.identifier_domain ?? "");
    if (type) decl.identifierType = type;
    if (value) decl.identifierValue = value;
    if (domain) decl.identifierDomain = domain;

    const declErrors = validateCoverage(decl);
    if (declErrors.length > 0) errors.push(...declErrors);
    else decls.push(decl);
  }
  return decls;
}

/**
 * Parse splits JSON back into importable rows.
 *
 * The canonical shape is an object with "events" and an optional "coverage".
 * A bare array is accepted as events-only, which is what earlier exports wrote.
 */
export function parseSplitsJson(json: string): SplitParseResult {
  const errors: ParseError[] = [];
  let parsed: unknown;

  try {
    parsed = JSON.parse(json);
  } catch (e) {
    return {
      splits: [],
      coverage: [],
      errors: [{ rowIndex: 0, field: "file", message: `Invalid JSON: ${e instanceof Error ? e.message : String(e)}` }],
    };
  }

  let rawEvents: unknown;
  let coverageDecls: CoverageDecl[] = [];
  if (Array.isArray(parsed)) {
    rawEvents = parsed;
  } else if (typeof parsed === "object" && parsed !== null) {
    const obj = parsed as Record<string, unknown>;
    rawEvents = obj.events;
    coverageDecls = parseCoverageArray(obj.coverage, errors);
  }

  if (!Array.isArray(rawEvents)) {
    return {
      splits: [],
      coverage: [],
      errors: [
        ...errors,
        { rowIndex: 0, field: "file", message: 'Expected a JSON array, or an object with an "events" array' },
      ],
    };
  }
  const parsedEvents: unknown[] = rawEvents;

  const splits: CorporateSplitImportRow[] = [];

  for (let i = 0; i < parsedEvents.length; i++) {
    const item = parsedEvents[i];
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
    if (ac !== AssetClass.ASSET_CLASS_UNSPECIFIED) row.assetClass = ac;

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

  // Resolve declarations against the instruments the events actually name, so
  // a global fans out to one wire entry each.
  const instruments: InstrumentRef[] = splits.map((s) => ({
    identifierType: s.identifierType,
    identifierValue: s.identifierValue,
    identifierDomain: s.identifierDomain ?? "",
  }));
  const expanded = expandCoverage(coverageDecls, instruments);
  errors.push(...expanded.errors);
  const coverage = expanded.resolved.map((c) => create(ImportCorporateEventCoverageSchema, c));

  return { splits, coverage, errors };
}
