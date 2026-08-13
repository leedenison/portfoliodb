import { ArchivePart } from "@/gen/archive/v1/common_pb";

/** One row of the export menu. */
export interface ArchivePartOption {
  part: ArchivePart;
  label: string;
  /** What the part carries. */
  note: string;
  /**
   * Plugin config is the one part not selected by default: including it carries
   * live API keys, which makes the archive a secret and changes where it can
   * safely be kept.
   */
  defaultSelected: boolean;
}

/** The system archive export menu, in restore order. */
export const SYSTEM_ARCHIVE_PART_OPTIONS: ArchivePartOption[] = [
  {
    part: ArchivePart.INSTRUMENTS,
    label: "Instruments",
    note: "Security master, identifiers and the results of identifier lookups.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.PRICES,
    label: "Prices",
    note: "End-of-day bars with the coverage that says which dates were asked for.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.CORPORATE_EVENTS,
    label: "Corporate events",
    note: "Splits and cash dividends with their coverage.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.INFLATION_INDICES,
    label: "Inflation indices",
    note: "Monthly index values per currency, for real-terms performance.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.FETCH_BLOCKS,
    label: "Fetch blocks",
    note: "Providers deliberately stopped for an instrument, and why.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.UNHANDLED_EVENTS,
    label: "Unhandled corporate events",
    note: "The review queue, and the calls an admin has already made on it.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.PLUGIN_CONFIG,
    label: "Plugin config",
    note: "Which plugins are on, in what order, and how each is configured. Carries live API keys, which makes the file a secret.",
    defaultSelected: false,
  },
];

/** The user archive export menu, in restore order. */
export const USER_ARCHIVE_PART_OPTIONS: ArchivePartOption[] = [
  {
    part: ArchivePart.PREFERENCES,
    label: "Preferences",
    note: "Display currency, and the asset classes ignored on import.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.TXS,
    label: "Transactions",
    note: "Every posting, grouped as the broker converter grouped it. Nothing can rebuild the grouping, so it travels with them.",
    defaultSelected: true,
  },
  {
    part: ArchivePart.DECLARATIONS,
    label: "Holding declarations",
    note: "What you stated you held, and when. Nothing can refetch a declaration, and a rebuild without them loses every opening balance and every checkpoint.",
    defaultSelected: true,
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
  [ArchivePart.PREFERENCES]: "Preferences",
  [ArchivePart.TXS]: "Transactions",
  [ArchivePart.DECLARATIONS]: "Holding declarations",
};
