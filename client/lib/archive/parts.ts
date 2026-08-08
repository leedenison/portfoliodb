import { ArchivePart } from "@/gen/archive/v1/common_pb";

/**
 * One row of the export menu.
 *
 * `available` is false for a part the archive format does not carry yet. Those
 * rows are shown disabled rather than hidden, so the menu says what the archive
 * will hold rather than only what it holds today.
 */
export interface ArchivePartOption {
  /** Absent until the part exists in the format. */
  part?: ArchivePart;
  label: string;
  /** What the part carries, or why it is not available yet. */
  note: string;
  available: boolean;
  /**
   * Selected by default when available. Plugin config is the one part that is
   * not: including it carries live API keys, which makes the archive a secret
   * and changes where it can safely be kept.
   */
  defaultSelected: boolean;
}

/** The export menu, in restore order. */
export const ARCHIVE_PART_OPTIONS: ArchivePartOption[] = [
  {
    part: ArchivePart.INSTRUMENTS,
    label: "Instruments",
    note: "Security master, identifiers and the results of identifier lookups.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.PRICES,
    label: "Prices",
    note: "End-of-day bars with the coverage that says which dates were asked for.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.CORPORATE_EVENTS,
    label: "Corporate events",
    note: "Splits and cash dividends with their coverage.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.INFLATION_INDICES,
    label: "Inflation indices",
    note: "Monthly index values per currency, for real-terms performance.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.FETCH_BLOCKS,
    label: "Fetch blocks",
    note: "Providers deliberately stopped for an instrument, and why.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.UNHANDLED_EVENTS,
    label: "Unhandled corporate events",
    note: "The review queue, and the calls an admin has already made on it.",
    available: true,
    defaultSelected: true,
  },
  {
    part: ArchivePart.PLUGIN_CONFIG,
    label: "Plugin config",
    note: "Which plugins are on, in what order, and how each is configured. Carries live API keys, which makes the file a secret.",
    available: true,
    defaultSelected: false,
  },
];

/** Display names for the parts a job reports against. */
export const ARCHIVE_PART_LABELS: Record<number, string> = {
  [ArchivePart.INSTRUMENTS]: "Instruments",
  [ArchivePart.PRICES]: "Prices",
  [ArchivePart.CORPORATE_EVENTS]: "Corporate events",
  [ArchivePart.INFLATION_INDICES]: "Inflation indices",
  [ArchivePart.FETCH_BLOCKS]: "Fetch blocks",
  [ArchivePart.UNHANDLED_EVENTS]: "Unhandled corporate events",
  [ArchivePart.PLUGIN_CONFIG]: "Plugin config",
};
