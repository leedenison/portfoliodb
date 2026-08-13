"use client";

import { useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AppShell } from "@/app/components/app-shell";
import { ErrorAlert } from "@/app/components/error-alert";
import { ImportArchivePanel } from "@/app/components/archive/import-panel";
import { JobPartsTable } from "@/app/components/archive/job-parts";
import { useAuth } from "@/contexts/auth-context";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { exportUserArchive, getJob, importUserArchive, listJobs } from "@/lib/portfolio-api";
import { marshalUser, unmarshalUser } from "@/lib/archive/codec";
import { assembleUserArchive, userPartCounts } from "@/lib/archive/assemble";
import { USER_ARCHIVE_PART_OPTIONS } from "@/lib/archive/parts";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import { JobStatus } from "@/gen/api/v1/api_pb";

const USER_ARCHIVE_JOB_TYPE = "user_archive";

const IMPORT_NOTE = (
  <>
    The parts are applied in order on the server, so an import finishes whether or not this page
    stays open. Importing ignored asset classes replaces the rules you have now and removes the
    transactions they cover. Importing holding declarations restates the ones the file names and
    leaves the rest alone -- a declaration missing from a file is not a declaration you deleted.
  </>
);

/**
 * Export and import the signed-in user's own archive.
 *
 * A separate page from /admin/archive, and a separate file: this one carries the
 * user's own data and no shared reference data at all. Restoring it into an
 * instance whose instruments are not loaded is supported and correct -- the
 * postings resolve through the normal identifier path, merely slower -- so
 * nothing here presents that as an error.
 */
export default function UserArchivePage() {
  const { state } = useAuth();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<ArchivePart>>(
    () => new Set(USER_ARCHIVE_PART_OPTIONS.filter((o) => o.defaultSelected).map((o) => o.part)),
  );
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [startedJobId, setStartedJobId] = useState<string | null>(null);

  // An import keeps running whether or not this page is open, so the job to
  // watch is looked up rather than remembered.
  const recent = useAuthedQuery({
    queryKey: qk.jobs(),
    queryFn: () => listJobs(null, USER_ARCHIVE_JOB_TYPE),
  });
  const jobId = startedJobId ?? recent.data?.jobs[0]?.id ?? null;

  const job = useAuthedQuery({
    queryKey: qk.job(jobId ?? ""),
    queryFn: () => getJob(jobId!),
    enabled: jobId !== null,
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
    () => USER_ARCHIVE_PART_OPTIONS.filter((o) => selected.has(o.part)).map((o) => o.part),
    [selected],
  );

  async function handleExport() {
    setExporting(true);
    setExportError(null);
    try {
      const items = [];
      for await (const item of exportUserArchive(parts)) {
        items.push(item);
      }
      const json = marshalUser(assembleUserArchive(items));
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "user-archive.json";
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

  // What an import changes is spread across the user's own pages, so once it is
  // done everything it could have touched is dropped rather than guessing which
  // part landed. Ignored asset classes delete transactions, which move holdings,
  // so those go too, and a restored declaration writes the pad posting that
  // opens its holding.
  const terminalJobId = job.data && !running ? jobId : null;
  useEffect(() => {
    if (terminalJobId === null) return;
    queryClient.invalidateQueries({ queryKey: qk.displayCurrency() });
    queryClient.invalidateQueries({ queryKey: qk.ignoredAssetClasses() });
    queryClient.invalidateQueries({ queryKey: qk.holdingDeclarations() });
    queryClient.invalidateQueries({ queryKey: qk.holdings() });
    queryClient.invalidateQueries({ queryKey: qk.txs() });
  }, [terminalJobId, queryClient]);

  return (
    <AppShell>
      <div data-testid="page-archive" className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && <p className="text-text-muted">Loading...</p>}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Your archive
            </h1>
            <p className="mt-3 text-text-muted">Sign in to export or import your data.</p>
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-2xl animate-fade-in space-y-5">
            <div>
              <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                Your archive
              </h2>
              <p className="mt-1 text-sm text-text-muted">
                Export and import your own data. The file carries none of the shared reference data
                the instance is built from, which is a separate archive an administrator keeps.
              </p>
            </div>

            <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-export">
              <h2 className="font-display text-base font-semibold text-text-primary">Export</h2>
              <p className="mt-1 text-sm text-text-muted">
                A part left unticked is absent from the file. A part ticked but holding nothing is
                written empty, which records that the export included it and there was nothing.
              </p>
              <ul className="mt-3 space-y-2">
                {USER_ARCHIVE_PART_OPTIONS.map((opt) => {
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

            <ImportArchivePanel
              running={running}
              onStarted={handleImportStarted}
              parse={unmarshalUser}
              counts={userPartCounts}
              submit={importUserArchive}
              note={IMPORT_NOTE}
            />

            {job.data && (
              <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-job">
                <h2 className="font-display text-base font-semibold text-text-primary">
                  Last import
                </h2>
                <p className="mt-1 text-sm text-text-muted">
                  {running
                    ? "Running. This continues whether or not this page is open."
                    : "Finished."}
                </p>
                <JobPartsTable job={job.data} />
              </section>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
