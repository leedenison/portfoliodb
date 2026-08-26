/**
 * Converter registry: brokers and formats (with optional convert + OptionsComponent).
 * Only brokers that have at least one format with a convert function appear in the upload broker list.
 */

import type { ComponentType } from "react";
import type { Broker } from "@/gen/type/v1/type_pb";
import type { StandardParseResult } from "@/lib/csv/parse-result";
import { statedIdentityErrors } from "@/lib/csv/stated-identity";

export interface ConverterOptionsProps {
  onOptionsChange: (opts: Record<string, unknown>) => void;
  options?: Record<string, unknown>;
}

export interface FormatEntry {
  id: string;
  label: string;
  /**
   * File input accept attribute (e.g. ".ofx,.qfx"). Required: a format that
   * omits it leaves the file dialog filtering for someone else's extension.
   */
  accept: string;
  convert?: (text: string, options?: Record<string, unknown>) => StandardParseResult;
  OptionsComponent?: ComponentType<ConverterOptionsProps>;
}

export interface BrokerEntry {
  broker: Broker;
  label: string;
  sourcePrefix: string;
  formats: FormatEntry[];
}

const registry: BrokerEntry[] = [];

/**
 * A converter with the self-contradiction check run over what it produced.
 *
 * Wrapped here rather than called by each converter so that a converter added
 * later cannot forget it. What makes an upload acceptable is not a property of
 * one broker's format, and a check every converter has to remember is one a
 * converter will eventually not remember.
 *
 * The errors are appended rather than replacing the converter's own: a file can
 * be both unreadable in places and self-contradicting, and the upload is refused
 * either way -- a non-empty errors list is what stops it.
 */
function checked(convert: NonNullable<FormatEntry["convert"]>): FormatEntry["convert"] {
  return (text, options) => {
    const result = convert(text, options);
    const stated = statedIdentityErrors(result.postings);
    if (stated.length === 0) return result;
    return { ...result, errors: [...result.errors, ...stated] };
  };
}

export function register(entry: BrokerEntry): void {
  registry.push({
    ...entry,
    formats: entry.formats.map((f) =>
      f.convert === undefined ? f : { ...f, convert: checked(f.convert) },
    ),
  });
}

/** Brokers that have at least one format with a convert function (for upload dropdown). */
export function getBrokerOptionsForUpload(): { value: Broker; label: string }[] {
  return registry
    .filter((e) => e.formats.some((f) => f.convert != null))
    .map((e) => ({ value: e.broker, label: e.label }));
}

export function getBrokerEntry(broker: Broker): BrokerEntry | undefined {
  return registry.find((e) => e.broker === broker);
}

/**
 * Format options for the selected broker: the archive document (no convert, so
 * the file is read rather than converted) plus the broker's own formats.
 */
export function getFormatsForBroker(broker: Broker): FormatEntry[] {
  const entry = getBrokerEntry(broker);
  if (!entry) return [];
  const archive: FormatEntry = { id: "archive", label: "Archive document", accept: ".json" };
  return [archive, ...entry.formats];
}

export function getBrokerLabel(broker: Broker): string {
  const entry = getBrokerEntry(broker);
  return entry?.label ?? "—";
}

export function getSourcePrefix(broker: Broker): string {
  const entry = getBrokerEntry(broker);
  return entry?.sourcePrefix ?? "unknown";
}
