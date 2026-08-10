/**
 * CSV line splitting, shared by the broker-format converters.
 *
 * Kept free of React so the import extension can use it.
 */

import Papa from "papaparse";

/** Parse a single CSV row into fields, handling quoted fields and escaped quotes. */
export function parseCSVLine(line: string): string[] {
  const result = Papa.parse(line, { header: false });
  return (result.data[0] as string[]) ?? [];
}
