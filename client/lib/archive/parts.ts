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
    label: "Unhandled event resolutions",
    note: "Not carried by the archive format yet.",
    available: false,
    defaultSelected: false,
  },
  {
    label: "Plugin config",
    note: "Not carried by the archive format yet. Will carry live API keys, so it will be off by default.",
    available: false,
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
};
