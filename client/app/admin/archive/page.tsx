"use client";

import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ErrorAlert } from "@/app/components/error-alert";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { exportSystemArchive, getJob, listJobs } from "@/lib/portfolio-api";
import { marshalSystem } from "@/lib/archive/codec";
import { assembleSystemArchive } from "@/lib/archive/assemble";
import { SYSTEM_ARCHIVE_PART_OPTIONS } from "@/lib/archive/parts";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import { JobStatus } from "@/gen/api/v1/api_pb";
import { ImportArchivePanel } from "./import-panel";
import { JobPartsTable } from "./job-parts";

const SYSTEM_ARCHIVE_JOB_TYPE = "system_archive";

export default function ArchivePage() {
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<ArchivePart>>(
    () => new Set(SYSTEM_ARCHIVE_PART_OPTIONS.filter((o) => o.defaultSelected).map((o) => o.part)),
  );
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [startedJobId, setStartedJobId] = useState<string | null>(null);

  // An import keeps running whether or not this page is open, so the job to
  // watch is looked up rather than remembered: closing the tab and coming back
  // must show the run that is still going.
  const recent = useAuthedQuery({
    queryKey: qk.jobs(),
    queryFn: () => listJobs(null, SYSTEM_ARCHIVE_JOB_TYPE),
  });
  const jobId = startedJobId ?? recent.data?.jobs[0]?.id ?? null;

  const job = useAuthedQuery({
    queryKey: qk.job(jobId ?? ""),
    queryFn: () => getJob(jobId!),
    enabled: jobId !== null,
    // Polling stops at a terminal status rather than running forever behind an
    // idle page.
    refetchInterval: (query) => {
      const s = query.state.data?.status;
      return s === JobStatus.SUCCESS || s === JobStatus.FAILED ? false : 2000;
    },
  });

  const running =
    job.data !== undefined &&
    job.data.status !== JobStatus.SUCCESS &&
    job.data.status !== JobStatus.FAILED;

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

  const parts = useMemo(
    () => SYSTEM_ARCHIVE_PART_OPTIONS.filter((o) => selected.has(o.part)).map((o) => o.part),
    [selected],
  );

  async function handleExport() {
    setExporting(true);
    setExportError(null);
    try {
      const items = [];
      for await (const item of exportSystemArchive(parts)) {
        items.push(item);
      }
      const json = marshalSystem(assembleSystemArchive(items));
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "system-archive.json";
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setExportError(errorMessage(e, "Export failed"));
    } finally {
      setExporting(false);
    }
  }

  function handleImportStarted(id: string) {
    setStartedJobId(id);
    queryClient.invalidateQueries({ queryKey: qk.jobs() });
  }

  // What an import changes is spread across the admin section, so once it is
  // done everything it could have touched is dropped rather than guessing which
  // part landed. Keyed on the terminal status so it runs once per finished job.
  const terminalJobId = job.data && !running ? jobId : null;
  useEffect(() => {
    if (terminalJobId === null) return;
    queryClient.invalidateQueries({ queryKey: qk.instruments() });
    queryClient.invalidateQueries({ queryKey: qk.prices() });
    queryClient.invalidateQueries({ queryKey: qk.corporateEventSplits() });
  }, [terminalJobId, queryClient]);

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-xl font-bold text-text-primary">Archive</h1>
        <p className="mt-1 text-sm text-text-muted">
          Export and import the system archive: the shared reference data this instance is built
          from. A user&apos;s own data is not reachable from here.
        </p>
      </div>

      <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-export">
        <h2 className="font-display text-base font-semibold text-text-primary">Export</h2>
        <p className="mt-1 text-sm text-text-muted">
          A part left unticked is absent from the file. A part ticked but holding nothing is written
          empty, which records that the export included it and there was nothing.
        </p>
        <ul className="mt-3 space-y-2">
          {SYSTEM_ARCHIVE_PART_OPTIONS.map((opt) => {
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

      <ImportArchivePanel running={running} onStarted={handleImportStarted} />

      {job.data && (
        <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-job">
          <h2 className="font-display text-base font-semibold text-text-primary">Last import</h2>
          <p className="mt-1 text-sm text-text-muted">
            {running
              ? "Running. This continues whether or not this page is open."
              : "Finished."}
          </p>
          <JobPartsTable job={job.data} />
        </section>
      )}
    </div>
  );
}
