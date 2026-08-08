/**
 * Coverage declarations in the corporate event JSON.
 *
 * A declaration is a half-open [from, before) span. It is either global -- no
 * identifier, applying to every instrument in the file -- or specific to one
 * instrument. The wire has no notion of a global: ImportCorporateEventCoverage
 * names an instrument, so expandCoverage resolves the global against the file's
 * contents before the request is built.
 *
 * The archive states coverage inside the group it applies to and needs none of
 * this; prices moved there under 0081 and this goes when corporate events
 * follow.
 *
 * See docs/spec/csv-format.md for the syntax.
 */

import type { ParseError } from "@/lib/csv/standard";
import { VALID_IDENTIFIER_TYPES } from "@/lib/identifiers";

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** An instrument named by its identifier triple. */
export interface InstrumentRef {
  identifierType: string;
  identifierValue: string;
  identifierDomain: string;
}

/**
 * One coverage declaration as written in a file. The identifier is either
 * wholly absent, making the declaration global, or names one instrument.
 */
export interface CoverageDecl {
  identifierType?: string;
  identifierValue?: string;
  identifierDomain?: string;
  from: string;
  before: string;
  /** Where the declaration was written, for error reporting. */
  rowIndex: number;
}

/** A declaration resolved to exactly one instrument, ready for the wire. */
export interface ResolvedCoverage extends InstrumentRef {
  from: string;
  before: string;
}

/** True when the declaration names no instrument and so applies to all of them. */
export function isGlobal(c: CoverageDecl): boolean {
  return !c.identifierType && !c.identifierValue && !c.identifierDomain;
}

function refKey(r: Partial<InstrumentRef>): string {
  return `${r.identifierType ?? ""}\x00${r.identifierValue ?? ""}\x00${r.identifierDomain ?? ""}`;
}

/**
 * Validate one declaration. The only place the half-open convention and the
 * all-or-nothing identifier rule are enforced.
 */
export function validateCoverage(c: CoverageDecl): ParseError[] {
  const errors: ParseError[] = [];
  const err = (field: string, message: string) =>
    errors.push({ rowIndex: c.rowIndex, field, message });

  if (!DATE_RE.test(c.from)) {
    err("from", `Invalid date "${c.from}": expected YYYY-MM-DD`);
  }
  if (!DATE_RE.test(c.before)) {
    err("before", `Invalid date "${c.before}": expected YYYY-MM-DD`);
  }
  // ISO dates compare correctly as strings. The span is half-open, so an empty
  // span declares nothing and is more likely a typo than an intent.
  if (errors.length === 0 && c.before <= c.from) {
    err("before", `"${c.before}" must be after "${c.from}": the span is half-open [from, before)`);
  }

  if (!isGlobal(c)) {
    // A partly-written identifier is a mistake, not a global declaration.
    if (!c.identifierType) {
      err("identifier_type", "Required unless the declaration is global (no identifier at all)");
    } else if (!VALID_IDENTIFIER_TYPES.has(c.identifierType)) {
      err("identifier_type", `Unknown identifier_type: ${c.identifierType}`);
    }
    if (!c.identifierValue) {
      err("identifier_value", "Required unless the declaration is global (no identifier at all)");
    }
  }

  return errors;
}

/**
 * Resolve declarations against the instruments a file actually names, yielding
 * one entry per instrument per span.
 *
 * A global applies to every instrument except those carrying a specific
 * declaration, which overrides it outright rather than adding to it. Several
 * specific declarations for one instrument all survive, since the wire accepts
 * more than one span per instrument.
 *
 * A specific declaration naming an instrument the file has no rows for is kept.
 * That is meaningful for corporate events, where coverage records what was
 * asked about rather than what came back, and a no-op for prices, where the
 * server only fills instruments the request supplies rows for.
 */
export function expandCoverage(
  decls: CoverageDecl[],
  instruments: InstrumentRef[],
): { resolved: ResolvedCoverage[]; errors: ParseError[] } {
  const errors: ParseError[] = [];
  const globals = decls.filter(isGlobal);
  const specifics = decls.filter((c) => !isGlobal(c));

  // Which of two globals wins would be arbitrary, so refuse to choose.
  for (const extra of globals.slice(1)) {
    errors.push({
      rowIndex: extra.rowIndex,
      field: "coverage",
      message: "Only one global coverage declaration is allowed per file",
    });
  }
  const global = globals.length === 1 ? globals[0] : undefined;

  const specificsByKey = new Map<string, CoverageDecl[]>();
  for (const s of specifics) {
    const key = refKey(s);
    const existing = specificsByKey.get(key);
    if (existing) existing.push(s);
    else specificsByKey.set(key, [s]);
  }

  const resolved: ResolvedCoverage[] = [];
  const seenKeys = new Set<string>();
  for (const inst of instruments) {
    const key = refKey(inst);
    if (seenKeys.has(key)) continue;
    seenKeys.add(key);

    const own = specificsByKey.get(key);
    if (own) {
      for (const s of own) resolved.push({ ...inst, from: s.from, before: s.before });
    } else if (global) {
      resolved.push({ ...inst, from: global.from, before: global.before });
    }
  }

  // Declarations for instruments the file has no rows for.
  for (const s of specifics) {
    if (seenKeys.has(refKey(s))) continue;
    resolved.push({
      identifierType: s.identifierType ?? "",
      identifierValue: s.identifierValue ?? "",
      identifierDomain: s.identifierDomain ?? "",
      from: s.from,
      before: s.before,
    });
  }

  return { resolved, errors };
}
