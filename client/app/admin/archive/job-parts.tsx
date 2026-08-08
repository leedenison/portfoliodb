"use client";

import { ARCHIVE_PART_LABELS } from "@/lib/archive/parts";
import { JobStatus } from "@/gen/api/v1/api_pb";
import type { GetJobResult, JobPartResult } from "@/lib/portfolio-api";

const STATUS_LABELS: Record<number, string> = {
  [JobStatus.PENDING]: "Waiting",
  [JobStatus.RUNNING]: "Running",
  [JobStatus.SUCCESS]: "Done",
  [JobStatus.FAILED]: "Failed",
};

/**
 * The per-part results of one import.
 *
 * A part with validation errors has still succeeded: what failed is a row. So
 * the count of rejected rows is shown beside the status rather than instead of
 * it, and "Done, 12 rejected" is a result the page can state plainly.
 */
export function JobPartsTable({ job }: { job: GetJobResult }) {
  if (job.parts.length === 0) {
    return <p className="mt-3 text-sm text-text-muted">This job carried no parts.</p>;
  }
  return (
    <div className="mt-3 space-y-3">
      <table className="w-full border-collapse text-sm" data-testid="job-parts">
        <thead>
          <tr className="border-b border-border">
            <th className="px-2 py-1.5 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">Part</th>
            <th className="px-2 py-1.5 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">Status</th>
            <th className="px-2 py-1.5 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">Progress</th>
            <th className="px-2 py-1.5 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">Rejected</th>
          </tr>
        </thead>
        <tbody>
          {job.parts.map((p) => (
            <PartRow key={p.part} part={p} />
          ))}
        </tbody>
      </table>

      {job.parts
        .filter((p) => p.validationErrors.length > 0 || p.message !== "")
        .map((p) => (
          <PartProblems key={`problems-${p.part}`} part={p} />
        ))}

      {job.validationErrors.length > 0 && (
        <div className="space-y-1">
          <p className="text-sm font-medium text-accent-dark">
            Problems with the file itself
          </p>
          <ul className="list-inside list-disc text-xs text-accent-dark">
            {job.validationErrors.map((e, i) => (
              <li key={i}>
                {e.field}: {e.message}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function PartRow({ part }: { part: JobPartResult }) {
  const failed = part.status === JobStatus.FAILED;
  return (
    <tr className="border-b border-border/40 last:border-0">
      <td className="px-2 py-1.5 text-text-primary">
        {ARCHIVE_PART_LABELS[part.part] ?? `Part ${part.part}`}
      </td>
      <td className={`px-2 py-1.5 ${failed ? "font-medium text-accent-dark" : "text-text-muted"}`}>
        {STATUS_LABELS[part.status] ?? "Unknown"}
      </td>
      <td className="px-2 py-1.5 text-right font-mono text-text-muted">
        {part.processedCount.toLocaleString()} / {part.totalCount.toLocaleString()}
      </td>
      <td className="px-2 py-1.5 text-right font-mono text-text-muted">
        {part.validationErrors.length.toLocaleString()}
      </td>
    </tr>
  );
}

function PartProblems({ part }: { part: JobPartResult }) {
  return (
    <div className="space-y-1">
      <p className="text-sm font-medium text-accent-dark">
        {ARCHIVE_PART_LABELS[part.part] ?? `Part ${part.part}`}
      </p>
      {part.message !== "" && <p className="text-xs text-accent-dark">{part.message}</p>}
      {part.validationErrors.length > 0 && (
        <div className="max-h-48 overflow-y-auto rounded-md border border-border">
          <table className="w-full border-collapse text-xs">
            <thead>
              <tr className="border-b border-border bg-primary-dark/3">
                <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Group</th>
                <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Field</th>
                <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Error</th>
              </tr>
            </thead>
            <tbody>
              {part.validationErrors.map((e, i) => (
                <tr key={i} className="border-b border-border/40 last:border-0">
                  <td className="px-3 py-1.5 font-mono text-text-muted">{e.rowIndex}</td>
                  <td className="px-3 py-1.5 font-mono text-text-primary">{e.field}</td>
                  <td className="px-3 py-1.5 text-accent-dark">{e.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
