/**
 * Converter registry: brokers and formats (with optional convert + OptionsComponent).
 * Only brokers that have at least one format with a convert function appear in the upload broker list.
 */

import type { ComponentType } from "react";
import type { Broker } from "@/gen/api/v1/api_pb";
import type { StandardParseResult } from "@/lib/csv/standard";

export interface ConverterOptionsProps {
  onOptionsChange: (opts: Record<string, unknown>) => void;
  options?: Record<string, unknown>;
}

export interface FormatEntry {
  id: string;
  label: string;
  /** File input accept attribute (e.g. ".ofx,.qfx"). Defaults to ".csv". */
  accept?: string;
  convert?: (csvText: string, options?: Record<string, unknown>) => StandardParseResult;
  OptionsComponent?: ComponentType<ConverterOptionsProps>;
}

export interface BrokerEntry {
  broker: Broker;
  label: string;
  sourcePrefix: string;
  formats: FormatEntry[];
}

const registry: BrokerEntry[] = [];

export function register(entry: BrokerEntry): void {
  registry.push(entry);
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

/** Format options for the selected broker: Standard (no convert) plus registered formats. */
export function getFormatsForBroker(broker: Broker): FormatEntry[] {
  const entry = getBrokerEntry(broker);
  if (!entry) return [];
  const standard: FormatEntry = { id: "standard", label: "Standard" };
  return [standard, ...entry.formats];
}

export function getBrokerLabel(broker: Broker): string {
  const entry = getBrokerEntry(broker);
  return entry?.label ?? "—";
}

export function getSourcePrefix(broker: Broker): string {
  const entry = getBrokerEntry(broker);
  return entry?.sourcePrefix ?? "unknown";
}
