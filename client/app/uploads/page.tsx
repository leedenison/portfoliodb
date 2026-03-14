"use client";

import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { useAuth } from "@/contexts/auth-context";
import { useUploadModal } from "@/contexts/upload-modal-context";
import { usePagination } from "@/hooks/use-pagination";
import { listJobs, getJob } from "@/lib/portfolio-api";
import type { JobSummary, GetJobResult } from "@/lib/portfolio-api";
import { JobStatus } from "@/gen/api/v1/api_pb";

const STATUS_LABEL: Record<number, string> = {
  [JobStatus.PENDING]: "Pending",
  [JobStatus.RUNNING]: "Running",
  [JobStatus.SUCCESS]: "Success",
  [JobStatus.FAILED]: "Failed",
};

const STATUS_STYLE: Record<number, string> = {
  [JobStatus.PENDING]: "bg-primary-dark/10 text-primary-dark",
  [JobStatus.RUNNING]: "bg-primary-dark/10 text-primary-dark",
  [JobStatus.SUCCESS]: "bg-green-100 text-green-800",
  [JobStatus.FAILED]: "bg-accent-soft/60 text-accent-dark",
};

export default function UploadsPage() {
  const { state, authError } = useAuth();
  const { openUploadModal } = useUploadModal();
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [expandedDetail, setExpandedDetail] = useState<GetJobResult | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const fetchJobs = useCallback(
    async (pageToken: string | null) => {
      const result = await listJobs(pageToken);
      return {
        items: result.jobs,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
    []
  );

  const {
    items: jobs,
    totalCount,
    loading,
    error,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
    refresh,
  } = usePagination(fetchJobs);

  // Fetch error details when a row is expanded.
  useEffect(() => {
    if (!expandedId) {
      setExpandedDetail(null);
      return;
    }
    let cancelled = false;
    setDetailLoading(true);
    getJob(expandedId)
      .then((result) => {
        if (!cancelled) setExpandedDetail(result);
      })
      .catch(() => {
        if (!cancelled) setExpandedDetail(null);
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [expandedId]);

  if (state.status === "loading") {
    return (
      <AppShell>
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-text-muted">Loading...</p>
        </div>
      </AppShell>
    );
  }

  if (state.status === "unauthenticated") {
    return (
      <AppShell>
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8 text-center">
          <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
            Uploads
          </h1>
          <p className="mt-3 text-text-muted">Sign in to view uploads.</p>
          {authError && (
            <p className="mt-4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
              {authError}
            </p>
          )}
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell>
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        <div className="w-full max-w-4xl animate-fade-in space-y-5">
          <div className="flex flex-wrap items-baseline justify-between gap-3">
            <div className="flex items-baseline gap-3">
              <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                Uploads
              </h2>
              {!loading && (
                <span className="font-mono text-xs text-text-muted">
                  {totalCount} total
                </span>
              )}
            </div>
            <button
              type="button"
              onClick={() => openUploadModal(() => refresh())}
              className="rounded-md bg-accent px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
            >
              Upload Transactions
            </button>
          </div>

          {loading && <p className="text-text-muted">Loading uploads...</p>}
          {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
          {!loading && !error && (
            <>
              <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
                <table className="w-full min-w-[480px] border-collapse text-sm">
                  <thead>
                    <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Filename
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Broker
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Status
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Date
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Errors
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {jobs.length === 0 ? (
                      <tr>
                        <td
                          colSpan={5}
                          className="px-4 py-8 text-center text-text-muted"
                        >
                          No uploads yet.
                        </td>
                      </tr>
                    ) : (
                      jobs.map((job) => {
                        const expanded = expandedId === job.id;
                        const errorCount =
                          job.validationErrorCount +
                          job.identificationErrorCount;
                        return (
                          <JobRow
                            key={job.id}
                            job={job}
                            errorCount={errorCount}
                            expanded={expanded}
                            detail={expanded ? expandedDetail : null}
                            detailLoading={expanded && detailLoading}
                            onToggle={() =>
                              setExpandedId(expanded ? null : job.id)
                            }
                          />
                        );
                      })
                    )}
                  </tbody>
                </table>
              </div>

              <PaginationControls
                pageIndex={pageIndex}
                hasPrev={hasPrev}
                hasNext={hasNext}
                onPrev={goPrev}
                onNext={goNext}
              />
            </>
          )}
        </div>
      </div>
    </AppShell>
  );
}

function JobRow({
  job,
  errorCount,
  expanded,
  detail,
  detailLoading,
  onToggle,
}: {
  job: JobSummary;
  errorCount: number;
  expanded: boolean;
  detail: GetJobResult | null;
  detailLoading: boolean;
  onToggle: () => void;
}) {
  return (
    <>
      <tr
        className="group cursor-pointer border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
        onClick={onToggle}
      >
        <td className="px-4 py-3 font-medium text-text-primary">
          {job.filename || <span className="italic text-text-muted">--</span>}
        </td>
        <td className="px-4 py-3 text-text-muted">{job.broker}</td>
        <td className="px-4 py-3">
          <span
            className={
              "inline-block rounded px-1.5 py-0.5 text-xs font-medium " +
              (STATUS_STYLE[job.status] ?? "bg-border text-text-muted")
            }
          >
            {STATUS_LABEL[job.status] ?? "Unknown"}
          </span>
        </td>
        <td className="px-4 py-3 text-text-muted">
          {job.createdAt?.toLocaleDateString() ?? "--"}
        </td>
        <td className="px-4 py-3 text-text-muted">
          {errorCount > 0 ? (
            <span className="font-medium text-accent-dark">{errorCount}</span>
          ) : (
            "0"
          )}
        </td>
      </tr>

      {expanded && (
        <tr className="border-b border-border/40">
          <td colSpan={5} className="px-4 py-4">
            {detailLoading ? (
              <p className="text-sm text-text-muted">Loading errors...</p>
            ) : detail ? (
              <ErrorDetail detail={detail} />
            ) : (
              <p className="text-sm text-text-muted">
                Unable to load error details.
              </p>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

function ErrorDetail({ detail }: { detail: GetJobResult }) {
  const hasValidation = detail.validationErrors.length > 0;
  const hasIdentification = detail.identificationErrors.length > 0;

  if (!hasValidation && !hasIdentification) {
    return <p className="text-sm text-text-muted">No errors.</p>;
  }

  return (
    <div className="space-y-4">
      {hasValidation && (
        <div>
          <h4 className="text-xs font-semibold uppercase tracking-wider text-text-muted">
            Validation errors
          </h4>
          <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
            {detail.validationErrors.map((e, i) => (
              <li key={i}>
                Row {e.rowIndex + 1}: {e.field} &ndash; {e.message}
              </li>
            ))}
          </ul>
        </div>
      )}
      {hasIdentification && (
        <div>
          <h4 className="text-xs font-semibold uppercase tracking-wider text-text-muted">
            Identification errors
          </h4>
          <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
            {detail.identificationErrors.map((e, i) => (
              <li key={i}>
                Row {e.rowIndex + 1}: {e.instrumentDescription} &ndash;{" "}
                {e.message}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
