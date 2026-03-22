"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { usePagination } from "@/hooks/use-pagination";
import {
  listPrices,
  listPriceFetchBlocks,
  deletePriceFetchBlock,
  exportPrices,
} from "@/lib/portfolio-api";
import { pricesToCsv } from "@/lib/csv/prices";
import { ImportPricesModal } from "./import-modal";
import type { EODPriceProto, ExportPriceRow, PriceFetchBlock } from "@/gen/api/v1/api_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";

type Tab = "prices" | "blocks";

export default function AdminPricesPage() {
  const [tab, setTab] = useState<Tab>("prices");

  return (
    <div className="space-y-5">
      <h1 className="font-display text-xl font-bold text-text-primary">
        Prices
      </h1>

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-border">
        <TabButton active={tab === "prices"} onClick={() => setTab("prices")}>
          Prices
        </TabButton>
        <TabButton active={tab === "blocks"} onClick={() => setTab("blocks")}>
          Price Fetch Blocks
        </TabButton>
      </div>

      {tab === "prices" ? <PriceListTab /> : <PriceFetchBlocksTab />}
    </div>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        "px-4 py-2 text-sm font-medium transition-colors " +
        (active
          ? "border-b-2 border-primary text-primary-dark"
          : "text-text-muted hover:text-text-primary")
      }
    >
      {children}
    </button>
  );
}

function fmtPrice(v: number | undefined): string {
  if (v === undefined) return "\u2014";
  return v.toFixed(2);
}

function fmtVolume(v: bigint | undefined): string {
  if (v === undefined) return "\u2014";
  return Number(v).toLocaleString();
}

// --- Prices Tab ---

function PriceListTab() {
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [exportLoading, setExportLoading] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(debounceRef.current);
  }, [search]);

  async function handleExport() {
    setExportLoading(true);
    setExportError(null);
    try {
      const rows: ExportPriceRow[] = [];
      for await (const row of exportPrices()) {
        rows.push(row);
      }
      const csv = pricesToCsv(rows);
      const blob = new Blob([csv], { type: "text/csv" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "prices.csv";
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setExportError(err instanceof Error ? err.message : String(err));
    } finally {
      setExportLoading(false);
    }
  }

  const fetchPrices = useCallback(
    async (pageToken: string | null) => {
      const result = await listPrices({
        search: debouncedSearch,
        dateFrom: dateFrom || undefined,
        dateTo: dateTo || undefined,
        pageToken,
      });
      return {
        items: result.prices,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [debouncedSearch, dateFrom, dateTo, refreshKey]
  );

  const {
    items: prices,
    totalCount,
    loading,
    error,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
  } = usePagination(fetchPrices);

  return (
    <div className="space-y-4">
      {/* Header with export/import buttons */}
      <div className="flex flex-wrap items-end gap-3">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by instrument..."
          className="w-full max-w-sm rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
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
            onClick={handleExport}
            disabled={exportLoading}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {exportLoading ? "Exporting..." : "Export CSV"}
          </button>
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15"
          >
            Import CSV
          </button>
        </div>
      </div>
      {exportError && <ErrorAlert>{exportError}</ErrorAlert>}

      {loading && <p className="text-text-muted">Loading prices...</p>}
      {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
      {!loading && !error && (
        <>
          <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
            <table className="w-full min-w-[700px] border-collapse text-sm">
              <thead>
                <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Instrument
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Date
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Open
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    High
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Low
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Close
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Adj Close
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Volume
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Provider
                  </th>
                </tr>
              </thead>
              <tbody>
                {prices.length === 0 ? (
                  <tr>
                    <td
                      colSpan={9}
                      className="px-4 py-8 text-center text-text-muted"
                    >
                      {debouncedSearch || dateFrom || dateTo
                        ? "No prices match your filters."
                        : "No price data yet."}
                    </td>
                  </tr>
                ) : (
                  prices.map((p) => (
                    <PriceRow key={`${p.instrumentId}-${p.priceDate}`} price={p} />
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

      <ImportPricesModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onComplete={() => setRefreshKey((k) => k + 1)}
      />
    </div>
  );
}

function PriceRow({ price: p }: { price: EODPriceProto }) {
  return (
    <tr className={
      "border-b border-border/40 last:border-0 hover:bg-primary-light/10" +
      (p.synthetic ? " opacity-60" : "")
    }>
      <td className="px-4 py-2 font-medium text-text-primary">
        {p.instrumentDisplayName}
      </td>
      <td className="px-4 py-2 text-text-muted">{p.priceDate}</td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtPrice(p.open)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtPrice(p.high)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtPrice(p.low)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-primary">
        {p.close.toFixed(2)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtPrice(p.adjustedClose)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtVolume(p.volume)}
      </td>
      <td className="px-4 py-2 text-text-muted">
        {p.dataProvider}
        {p.synthetic && (
          <span className="ml-2 inline-block rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium uppercase text-amber-700">
            Synthetic
          </span>
        )}
      </td>
    </tr>
  );
}

// --- Price Fetch Blocks Tab ---

function PriceFetchBlocksTab() {
  const [blocks, setBlocks] = useState<PriceFetchBlock[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [clearing, setClearing] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setBlocks(await listPriceFetchBlocks());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load blocks");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function handleClear(block: PriceFetchBlock) {
    const key = `${block.instrumentId}:${block.pluginId}`;
    setClearing(key);
    setError(null);
    try {
      await deletePriceFetchBlock(block.instrumentId, block.pluginId);
      setBlocks((prev) =>
        prev.filter(
          (b) =>
            b.instrumentId !== block.instrumentId ||
            b.pluginId !== block.pluginId
        )
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to clear block");
    } finally {
      setClearing(null);
    }
  }

  if (loading) {
    return <div className="text-text-muted">Loading price fetch blocks...</div>;
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-text-muted">
        Instruments blocked from specific price plugins due to permanent errors
        (e.g. HTTP 403, 404). Clear a block to allow the system to retry.
      </p>
      {error && <ErrorAlert>{error}</ErrorAlert>}
      {blocks.length === 0 ? (
        <p className="text-text-muted">No blocked instruments.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-text-muted">
                <th className="pb-2 pr-4 font-medium">Instrument</th>
                <th className="pb-2 pr-4 font-medium">Plugin</th>
                <th className="pb-2 pr-4 font-medium">Reason</th>
                <th className="pb-2 pr-4 font-medium">Blocked at</th>
                <th className="pb-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {blocks.map((block) => {
                const key = `${block.instrumentId}:${block.pluginId}`;
                return (
                  <tr key={key} className="border-b border-border/50">
                    <td className="py-2 pr-4 text-text-primary">
                      {block.instrumentDisplayName}
                    </td>
                    <td className="py-2 pr-4 text-text-primary">
                      {block.pluginDisplayName}
                    </td>
                    <td className="py-2 pr-4 text-text-muted">
                      {block.reason}
                    </td>
                    <td className="py-2 pr-4 text-text-muted">
                      {block.createdAt
                        ? timestampDate(block.createdAt).toLocaleDateString()
                        : ""}
                    </td>
                    <td className="py-2 text-right">
                      <button
                        type="button"
                        onClick={() => handleClear(block)}
                        disabled={clearing === key}
                        className="rounded border border-border px-3 py-1 text-xs hover:bg-background disabled:opacity-50"
                      >
                        {clearing === key ? "Clearing..." : "Clear"}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
