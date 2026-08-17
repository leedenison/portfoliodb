"use client";

import { useState } from "react";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import { listWorkers, triggerPriceFetch, triggerInflationFetch, WorkerState, type WorkerRow } from "@/lib/portfolio-api";

function stateLabel(state: WorkerState): string {
  switch (state) {
    case WorkerState.IDLE:
      return "Idle";
    case WorkerState.RUNNING:
      return "Running";
    default:
      return "Unknown";
  }
}

function stateBadge(state: WorkerState): string {
  switch (state) {
    case WorkerState.IDLE:
      return "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400";
    case WorkerState.RUNNING:
      return "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400";
    default:
      return "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400";
  }
}

export default function AdminWorkersPage() {
  const [triggerError, setTriggerError] = useState<string | null>(null);
  const [triggering, setTriggering] = useState<string | null>(null);

  // refetchInterval rather than a setInterval the effect has to tear down. The
  // old version had no cancel flag either, so a response arriving after unmount
  // called setState on a dead component.
  const {
    data: workers = [],
    isPending: loading,
    isFetching,
    refetch,
    error: loadError,
  } = useAuthedQuery<WorkerRow[]>({
    queryKey: qk.workers(),
    queryFn: listWorkers,
    refetchInterval: 2000,
  });

  const error =
    triggerError ?? (loadError ? errorMessage(loadError, "Failed to load workers") : null);

  const triggerFns: Record<string, () => Promise<void>> = {
    price_fetcher: triggerPriceFetch,
    inflation_fetcher: triggerInflationFetch,
  };

  async function handleTrigger(name: string, fn: () => Promise<void>) {
    setTriggering(name);
    setTriggerError(null);
    try {
      await fn();
    } catch (e) {
      setTriggerError(errorMessage(e, `Failed to trigger ${name}`));
    } finally {
      setTriggering(null);
    }
  }

  if (loading && workers.length === 0) {
    return (
      <div>
        <h1 className="font-display text-xl font-bold text-text-primary">Workers</h1>
        <p className="mt-2 text-text-muted">Loading workers...</p>
      </div>
    );
  }

  return (
    <div data-testid="page-workers">
      <div className="flex items-center justify-between gap-4">
        <h1 className="font-display text-xl font-bold text-text-primary">Workers</h1>
        <button
          type="button"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="rounded-sm bg-primary px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-dark disabled:opacity-50"
        >
          {isFetching ? "Refreshing..." : "Refresh"}
        </button>
      </div>
      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}
      <p className="mt-1 text-sm text-text-muted">
        Background worker status. Refreshes every 2 seconds.
      </p>
      {workers.length === 0 && !error ? (
        <p className="mt-4 text-text-muted">No workers registered.</p>
      ) : (
        <table data-testid="workers-table" className="mt-4 w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border text-text-muted">
              <th className="py-2 pr-4 font-medium">Name</th>
              <th className="py-2 pr-4 font-medium">State</th>
              <th className="py-2 pr-4 font-medium">Summary</th>
              <th className="py-2 pr-4 font-medium">Queue</th>
              <th className="py-2 pr-4 font-medium">Cycles</th>
              <th className="py-2 pr-4 font-medium">Updated</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {workers.map((w) => (
              <tr key={w.name} data-testid="worker-row" className="border-b border-border">
                <td className="py-2 pr-4 font-mono text-text-primary">{w.name}</td>
                <td className="py-2 pr-4">
                  <span
                    data-worker-name={w.name}
                    data-worker-state={stateLabel(w.state).toLowerCase()}
                    data-worker-cycles={w.cycles}
                    className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${stateBadge(w.state)}`}
                  >
                    {stateLabel(w.state)}
                  </span>
                </td>
                <td className="py-2 pr-4 text-text-muted">{w.summary || "\u2014"}</td>
                <td className="py-2 pr-4 tabular-nums text-text-secondary">{w.queueDepth}</td>
                <td className="py-2 pr-4 tabular-nums text-text-secondary">{w.cycles}</td>
                <td className="py-2 pr-4 text-text-muted">
                  {w.updatedAt ? w.updatedAt.toLocaleTimeString() : "\u2014"}
                </td>
                <td className="py-2 text-right">
                  {triggerFns[w.name] && (
                    <button
                      type="button"
                      onClick={() => handleTrigger(w.name, triggerFns[w.name])}
                      disabled={triggering !== null}
                      className="rounded-sm border border-border px-3 py-1 text-xs hover:bg-background disabled:opacity-50"
                    >
                      {triggering === w.name ? "Triggering..." : "Trigger"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
