"use client";

import { useMemo, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { errorMessage } from "@/lib/errors";
import type { ArchivePartOption } from "@/lib/archive/parts";
import { ArchivePart } from "@/gen/archive/v1/common_pb";

/**
 * Choose what an archive carries, and download it.
 *
 * Which parts a menu offers differs between the two archives; what a menu means
 * does not, which is why both pages share this. A part left unticked is absent
 * from the file, and a part ticked but holding nothing is written empty, which
 * records that the export included it and there was nothing.
 *
 * `build` returns the document text, so the streaming, assembly and marshalling
 * of one kind of archive stay with the page that knows which kind it is. That is
 * the split ImportArchivePanel already makes with parse and submit.
 */
export function ExportArchivePanel({
  options,
  build,
  filename,
}: {
  /** The parts on offer, in restore order. */
  options: ArchivePartOption[];
  /** Produce the document text for the parts asked for. */
  build: (parts: ArchivePart[]) => Promise<string>;
  /** What the downloaded file is called. */
  filename: string;
}) {
  const [selected, setSelected] = useState<Set<ArchivePart>>(
    () => new Set(options.filter((o) => o.defaultSelected).map((o) => o.part)),
  );
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);

  function toggle(part: ArchivePart) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(part)) {
        next.delete(part);
      } else {
        next.add(part);
      }
      return next;
    });
  }

  // Menu order rather than the order the boxes were ticked in.
  const parts = useMemo(
    () => options.filter((o) => selected.has(o.part)).map((o) => o.part),
    [options, selected],
  );

  async function handleExport() {
    setExporting(true);
    setExportError(null);
    try {
      const json = await build(parts);
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setExportError(errorMessage(e, "Export failed"));
    } finally {
      setExporting(false);
    }
  }

  return (
    <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-export">
      <h2 className="font-display text-base font-semibold text-text-primary">Export</h2>
      <p className="mt-1 text-sm text-text-muted">
        A part left unticked is absent from the file. A part ticked but holding nothing is written
        empty, which records that the export included it and there was nothing.
      </p>
      <ul className="mt-3 space-y-2">
        {options.map((opt) => {
          const id = `part-${opt.part}`;
          return (
            <li key={id} className="flex items-start gap-2.5">
              <input
                id={id}
                type="checkbox"
                className="mt-0.5 h-4 w-4 shrink-0 accent-primary"
                checked={selected.has(opt.part)}
                onChange={() => toggle(opt.part)}
              />
              <label htmlFor={id}>
                <span className="text-sm font-medium text-text-primary">{opt.label}</span>
                <span className="block text-xs text-text-muted">{opt.note}</span>
              </label>
            </li>
          );
        })}
      </ul>
      {exportError && (
        <div className="mt-3">
          <ErrorAlert>{exportError}</ErrorAlert>
        </div>
      )}
      <button
        type="button"
        onClick={handleExport}
        disabled={exporting || parts.length === 0}
        data-testid="export-archive"
        className="mt-4 rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {exporting ? "Exporting…" : "Export archive"}
      </button>
    </section>
  );
}
