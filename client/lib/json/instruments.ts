/**
 * JSON serialization for instrument identity export/import.
 * Format: array of instrument objects, each with an identifiers array.
 * Derivatives reference their underlying by position via underlying_index.
 * No server UUIDs are included.
 */

import { create } from "@bufbuild/protobuf";
import {
  IdentifierType,
  InstrumentIdentifierSchema,
  InstrumentSchema,
} from "@/gen/api/v1/api_pb";
import type { Instrument } from "@/gen/api/v1/api_pb";
import type { ParseError } from "@/lib/csv/standard";

export interface InstrumentParseResult {
  instruments: Instrument[];
  errors: ParseError[];
}

// Build bidirectional maps between IdentifierType numeric values and their string names.
const IDENTIFIER_TYPE_NAME: Record<number, string> = {};
const IDENTIFIER_TYPE_BY_NAME: Record<string, IdentifierType> = {};

for (const [key, val] of Object.entries(IdentifierType)) {
  if (typeof val === "number" && val !== IdentifierType.IDENTIFIER_TYPE_UNSPECIFIED) {
    IDENTIFIER_TYPE_NAME[val] = key;
    IDENTIFIER_TYPE_BY_NAME[key] = val as IdentifierType;
  }
}

interface SerializedIdentifier {
  type: string;
  value: string;
  canonical: boolean;
  domain?: string;
}

interface SerializedInstrument {
  asset_class: string;
  exchange: string;
  currency: string;
  name: string;
  underlying_index?: number;
  identifiers: SerializedIdentifier[];
}

/** Serialize instruments to JSON string. */
export function instrumentsToJson(instruments: Instrument[]): string {
  // Collect all unique instruments (including nested underlyings) into a flat
  // array, preserving insertion order so underlying_index is stable.
  const ordered: Instrument[] = [];
  const indexById = new Map<string, number>();

  const collect = (inst: Instrument) => {
    if (indexById.has(inst.id)) return;
    // Collect underlying first so its index is lower.
    const underlying = inst.underlying ?? null;
    if (underlying) collect(underlying);
    indexById.set(inst.id, ordered.length);
    ordered.push(inst);
  };
  for (const inst of instruments) collect(inst);

  const serialized: SerializedInstrument[] = ordered.map((inst) => {
    const underlying = inst.underlying ?? null;
    const underlyingIndex = underlying != null ? indexById.get(underlying.id) : undefined;
    const obj: SerializedInstrument = {
      asset_class: inst.assetClass,
      exchange: inst.exchange,
      currency: inst.currency,
      name: inst.name,
      identifiers: inst.identifiers.map((id) => {
        const out: SerializedIdentifier = {
          type: IDENTIFIER_TYPE_NAME[id.type] ?? String(id.type),
          value: id.value,
          canonical: id.canonical,
        };
        if (id.domain) out.domain = id.domain;
        return out;
      }),
    };
    if (underlyingIndex !== undefined) obj.underlying_index = underlyingIndex;
    return obj;
  });

  return JSON.stringify(serialized, null, 2) + "\n";
}

/** Parse instruments JSON back into Instrument objects. */
export function jsonToInstruments(json: string): InstrumentParseResult {
  const errors: ParseError[] = [];
  let parsed: unknown;

  try {
    parsed = JSON.parse(json);
  } catch (e) {
    return {
      instruments: [],
      errors: [{ rowIndex: 0, field: "file", message: `Invalid JSON: ${e instanceof Error ? e.message : String(e)}` }],
    };
  }

  if (!Array.isArray(parsed)) {
    return {
      instruments: [],
      errors: [{ rowIndex: 0, field: "file", message: "Expected a JSON array" }],
    };
  }

  const instruments: Instrument[] = [];

  for (let i = 0; i < parsed.length; i++) {
    const item = parsed[i];
    if (typeof item !== "object" || item === null) {
      errors.push({ rowIndex: i, field: "item", message: "Expected an object" });
      instruments.push(create(InstrumentSchema, {})); // placeholder to keep indices stable
      continue;
    }
    const obj = item as Record<string, unknown>;
    const identifiers = [];
    const rawIds = Array.isArray(obj.identifiers) ? obj.identifiers : [];

    for (let j = 0; j < rawIds.length; j++) {
      const raw = rawIds[j] as Record<string, unknown>;
      const typeStr = String(raw.type ?? "").toUpperCase();
      const idType = IDENTIFIER_TYPE_BY_NAME[typeStr];
      if (idType === undefined) {
        errors.push({ rowIndex: i, field: `identifiers[${j}].type`, message: `Unknown identifier type: ${raw.type}` });
        continue;
      }
      const value = String(raw.value ?? "");
      if (!value) {
        errors.push({ rowIndex: i, field: `identifiers[${j}].value`, message: "Required" });
        continue;
      }
      identifiers.push(
        create(InstrumentIdentifierSchema, {
          type: idType,
          value,
          canonical: raw.canonical === true,
          domain: raw.domain ? String(raw.domain) : undefined,
        })
      );
    }

    instruments.push(
      create(InstrumentSchema, {
        assetClass: String(obj.asset_class ?? ""),
        exchange: String(obj.exchange ?? ""),
        currency: String(obj.currency ?? ""),
        name: String(obj.name ?? ""),
        identifiers,
      })
    );
  }

  // Resolve underlying_index references now that all instruments are built.
  for (let i = 0; i < parsed.length; i++) {
    const item = parsed[i] as Record<string, unknown>;
    if (typeof item !== "object" || item === null) continue;
    const idx = item.underlying_index;
    if (idx === undefined || idx === null) continue;
    const underlyingIdx = Number(idx);
    if (!Number.isInteger(underlyingIdx) || underlyingIdx < 0 || underlyingIdx >= instruments.length) {
      errors.push({ rowIndex: i, field: "underlying_index", message: `Index ${idx} out of range` });
      continue;
    }
    instruments[i].underlying = instruments[underlyingIdx];
  }

  return { instruments, errors };
}
