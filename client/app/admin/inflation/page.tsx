"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { usePagination } from "@/hooks/use-pagination";
import { listInflationIndices, triggerInflationFetch } from "@/lib/portfolio-api";
import type { InflationIndexProto } from "@/gen/api/v1/api_pb";

export default function AdminInflationPage() {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [fetchLoading, setFetchLoading] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(debounceRef.current);
  }, [search]);

  async function handleTriggerFetch() {
    setFetchLoading(true);
    setFetchError(null);
    try {
      await triggerInflationFetch();
      // Refresh after a short delay to allow the worker to start.
      setTimeout(() => setRefreshKey((k) => k + 1), 2000);
    } catch (err) {
      setFetchError(err instanceof Error ? err.message : String(err));
    } finally {
      setFetchLoading(false);
    }
  }

  const fetchIndices = useCallback(
    async (pageToken: string | null) => {
      const result = await listInflationIndices({
        currency: debouncedSearch || undefined,
        dateFrom: dateFrom || undefined,
        dateTo: dateTo || undefined,
        pageToken,
      });
      return {
        items: result.indices,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [debouncedSearch, dateFrom, dateTo, refreshKey]
  );

  const {
    items: indices,
    totalCount,
    loading,
    error,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
  } = usePagination(fetchIndices);

  return (
    <div className="space-y-5">
      <h1 className="font-display text-xl font-bold text-text-primary">
        Inflation Indices
      </h1>

      <div className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter by currency (e.g. GBP)..."
            className="w-full max-w-xs rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
          />
          <label className="flex flex-col gap-1 text-xs text-text-muted">
            From
            <input
              type="date"
              value={dateFrom}
              onChange={(e) => setDateFrom(e.target.value)}
              className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-text-muted">
            To
            <input
              type="date"
              value={dateTo}
              onChange={(e) => setDateTo(e.target.value)}
              className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
            />
          </label>
          {!loading && (
            <span className="font-mono text-xs text-text-muted">
              {totalCount} total
            </span>
          )}
          <div className="ml-auto flex gap-2">
            <button
              type="button"
              onClick={handleTriggerFetch}
              disabled={fetchLoading}
              className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {fetchLoading ? "Triggering..." : "Trigger Fetch"}
            </button>
          </div>
        </div>
        {fetchError && <ErrorAlert>{fetchError}</ErrorAlert>}

        {loading && <p className="text-text-muted">Loading inflation data...</p>}
        {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
        {!loading && !error && (
          <>
            <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
              <table className="w-full min-w-[500px] border-collapse text-sm">
                <thead>
                  <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                    <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                      Currency
                    </th>
                    <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                      Month
                    </th>
                    <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                      Index Value
                    </th>
                    <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                      Base Year
                    </th>
                    <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                      Provider
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {indices.length === 0 ? (
                    <tr>
                      <td
                        colSpan={5}
                        className="px-4 py-8 text-center text-text-muted"
                      >
                        {debouncedSearch || dateFrom || dateTo
                          ? "No inflation data matches your filters."
                          : "No inflation data yet."}
                      </td>
                    </tr>
                  ) : (
                    indices.map((idx) => (
                      <InflationRow
                        key={`${idx.currency}-${idx.month}`}
                        index={idx}
                      />
                    ))
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
  );
}

function InflationRow({ index: idx }: { index: InflationIndexProto }) {
  return (
    <tr className="border-b border-border/40 last:border-0 hover:bg-primary-light/10">
      <td className="px-4 py-2 font-medium text-text-primary">
        {idx.currency}
      </td>
      <td className="px-4 py-2 text-text-muted">{idx.month}</td>
      <td className="px-4 py-2 text-right font-mono text-text-primary">
        {idx.indexValue.toFixed(1)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {idx.baseYear}
      </td>
      <td className="px-4 py-2 text-text-muted">{idx.dataProvider}</td>
    </tr>
  );
}
