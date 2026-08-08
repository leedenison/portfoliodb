/**
 * Encode and decode PortfolioDB archive documents.
 *
 * The encoding decisions are made here and only here, and mirror
 * server/archive/codec.go: an archive written by one runtime has to be the same
 * bytes as one written by the other. See docs/spec/archive-format.md.
 *
 * React-free, so the browser extension can import it through its path alias.
 */

import { create, fromJson, toJsonString } from "@bufbuild/protobuf";
import type { DescEnum, DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf";
import {
  SystemArchiveSchema,
  UserArchiveSchema,
} from "@/gen/archive/v1/archive_pb";
import type { SystemArchive, UserArchive } from "@/gen/archive/v1/archive_pb";
import { ArchiveKind } from "@/gen/archive/v1/common_pb";

/** What this build writes, and the highest it will read. */
export const FORMAT_VERSION = 1;

const writeOpts = {
  // snake_case keys. Without this protobuf-es writes lowerCamelCase, which is a
  // total format change and a silent one.
  useProtoFieldName: true,
  // Enums by name: "STOCK", not 1.
  enumAsInteger: false,
  // Absent stays absent, which is the whole point of the optional fields.
  alwaysEmitImplicit: false,
} as const;

const readOpts = {
  // A field added by a later PortfolioDB is dropped rather than failing the
  // parse. Safe only because the format version is checked first.
  //
  // In protobuf-es this flag also swallows an unrecognised enum *value* name,
  // leaving the field at zero, where Go's protojson rejects it. That divergence
  // is not acceptable here -- a broker read as unspecified is a wrong value
  // rather than an error -- so assertKnownEnums restores the stricter
  // behaviour and the two runtimes agree.
  ignoreUnknownFields: true,
} as const;

/** An archive written by a later PortfolioDB than this build understands. */
export class ArchiveVersionError extends Error {
  constructor(
    readonly got: number,
    readonly supports: number
  ) {
    super(`archive format version ${got} is newer than this client supports (${supports})`);
    this.name = "ArchiveVersionError";
  }
}

/** A user archive handed to the system importer, or the reverse. */
export class ArchiveKindError extends Error {
  constructor(
    readonly got: string,
    readonly want: string
  ) {
    super(`archive is ${got}, expected ${want}`);
    this.name = "ArchiveKindError";
  }
}

/**
 * A sentence a user can act on, for an error thrown by unmarshalSystem or
 * unmarshalUser.
 *
 * Whether a file is a valid archive is an all-or-nothing question answered at
 * parse time, so this is the only thing an upload UI has to say about a
 * rejected file. Whether its contents resolve against this instance is a
 * separate, per-group question, answered later by the job's validation errors.
 */
export function archiveErrorMessage(e: unknown): string {
  if (e instanceof ArchiveVersionError) {
    return `This file was written by a later PortfolioDB (format version ${e.got}). Upgrade this instance to read it.`;
  }
  if (e instanceof ArchiveKindError) {
    return `This is a ${e.got.toLowerCase()} archive, not a ${e.want.toLowerCase()} one.`;
  }
  return e instanceof Error ? e.message : String(e);
}

/** Write a system archive. The envelope's version and kind are stamped here. */
export function marshalSystem(archive: MessageInitShape<typeof SystemArchiveSchema>): string {
  return marshal(SystemArchiveSchema, archive, ArchiveKind.SYSTEM);
}

/** Write a user archive. */
export function marshalUser(archive: MessageInitShape<typeof UserArchiveSchema>): string {
  return marshal(UserArchiveSchema, archive, ArchiveKind.USER);
}

/** Read a system archive. Throws ArchiveVersionError or ArchiveKindError. */
export function unmarshalSystem(json: string): SystemArchive {
  return unmarshal(SystemArchiveSchema, json, ArchiveKind.SYSTEM);
}

/** Read a user archive. */
export function unmarshalUser(json: string): UserArchive {
  return unmarshal(UserArchiveSchema, json, ArchiveKind.USER);
}

function marshal<Desc extends DescMessage>(
  schema: Desc,
  init: MessageInitShape<Desc>,
  kind: ArchiveKind
): string {
  const msg = create(schema, init) as MessageShape<Desc> & {
    envelope?: { formatVersion: number; kind: ArchiveKind };
  };
  // Stamped rather than taken from the caller, so no call site can write a
  // document that misdescribes its own version or kind. Created when absent, so
  // a caller cannot write one with no envelope at all either.
  const envelopeField = schema.fields.find((f) => f.name === "envelope");
  if (!msg.envelope && envelopeField?.fieldKind === "message") {
    (msg as { envelope?: unknown }).envelope = create(envelopeField.message);
  }
  const envelope = msg.envelope as { formatVersion: number; kind: ArchiveKind };
  envelope.formatVersion = FORMAT_VERSION;
  envelope.kind = kind;
  return toJsonString(schema, msg, writeOpts);
}

// The document is parsed once and the parsed value is reused: the envelope
// probe, the enum check and the message decode would otherwise each walk it,
// and a price archive is megabytes.
function unmarshal<Desc extends DescMessage>(
  schema: Desc,
  json: string,
  kind: ArchiveKind
): MessageShape<Desc> {
  let doc: unknown;
  try {
    doc = JSON.parse(json);
  } catch (e) {
    throw new Error(`not an archive document: ${e instanceof Error ? e.message : String(e)}`);
  }
  probe(doc, kind);
  assertKnownEnums(schema, doc, "");
  return fromJson(schema, doc as never, readOpts);
}

/**
 * Check the envelope before decoding the document, because decoding is what a
 * version mismatch would break: a reader that failed part-way would report a
 * confusing parse error where it should report that the file was written by a
 * later PortfolioDB.
 *
 * Both spellings of the key are accepted: protobuf-es writes snake_case and
 * reads either, so a hand-written document using lowerCamelCase parses.
 */
function probe(doc: unknown, want: ArchiveKind): void {
  const envelope = (doc as { envelope?: Record<string, unknown> } | null)?.envelope;
  if (!envelope) throw new Error("archive has no envelope");

  const raw = envelope.format_version ?? envelope.formatVersion;
  const version = typeof raw === "number" ? raw : Number(raw);
  if (!version || Number.isNaN(version)) throw new Error("archive has no format_version");
  if (version > FORMAT_VERSION) throw new ArchiveVersionError(version, FORMAT_VERSION);

  const kind = envelope.kind;
  if (typeof kind !== "string" || !(kind in ArchiveKind)) {
    throw new Error(`archive kind ${JSON.stringify(kind)} is not a known kind`);
  }
  if (ArchiveKind[kind as keyof typeof ArchiveKind] !== want) {
    throw new ArchiveKindError(kind, ArchiveKind[want]);
  }
}

/**
 * Walk a parsed document against its descriptor and reject any enum value the
 * vocabulary does not define. A name outside the vocabulary is not
 * representable anywhere in PortfolioDB, not merely in this file, so it is an
 * error rather than something to drop -- and dropping it would leave a zero,
 * which reads as a different value rather than as nothing.
 *
 * Unknown *fields* are still ignored; only unknown enum values are refused.
 */
function assertKnownEnums(schema: DescMessage, value: unknown, path: string): void {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return;
  const obj = value as Record<string, unknown>;

  for (const field of schema.fields) {
    const raw = obj[field.name] ?? obj[field.jsonName];
    if (raw === undefined || raw === null) continue;
    const at = path ? `${path}.${field.name}` : field.name;

    switch (field.fieldKind) {
      case "enum":
        checkEnum(field.enum, raw, at);
        break;
      case "message":
        assertKnownEnums(field.message, raw, at);
        break;
      case "list":
        if (!Array.isArray(raw)) break;
        raw.forEach((item, i) => {
          if (field.listKind === "enum") checkEnum(field.enum, item, `${at}[${i}]`);
          else if (field.listKind === "message") assertKnownEnums(field.message, item, `${at}[${i}]`);
        });
        break;
      case "map":
        for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
          if (field.mapKind === "enum") checkEnum(field.enum, v, `${at}[${k}]`);
          else if (field.mapKind === "message") assertKnownEnums(field.message, v, `${at}[${k}]`);
        }
        break;
    }
  }
}

function checkEnum(desc: DescEnum, value: unknown, at: string): void {
  // protojson accepts a value by number as well as by name.
  if (typeof value === "number") return;
  if (typeof value !== "string" || !desc.values.some((v) => v.name === value)) {
    throw new Error(`${at}: ${JSON.stringify(value)} is not a known ${desc.name}`);
  }
}
