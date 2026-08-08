"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { usePagination } from "@/hooks/use-pagination";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import {
  listPrices,
  listPriceFetchBlocks,
  deletePriceFetchBlock,
  exportPrices,
} from "@/lib/portfolio-api";
import { marshalAdmin } from "@/lib/archive/codec";
import { dayAfter } from "@/lib/dates";
import { useDebounce } from "@/hooks/use-debounce";
import { ImportPricesModal } from "./import-modal";
import type { EODPriceProto, PriceFetchBlock } from "@/gen/api/v1/api_pb";
import type { Envelope } from "@/gen/archive/v1/common_pb";
import type { PriceGroup } from "@/gen/archive/v1/prices_pb";
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

// Prices arrive as decimal strings and are rendered as supplied. Rounding to a
// fixed 2dp here would hide a price quoted to more places, which is the whole
// reason the column is exact.
function fmtPrice(v: string | undefined): string {
  return v === undefined ? "\u2014" : v;
}

function fmtVolume(v: bigint | undefined): string {
  if (v === undefined) return "\u2014";
  return Number(v).toLocaleString();
}

// --- Prices Tab ---

function PriceListTab() {
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounce(search);
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [exportLoading, setExportLoading] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const queryClient = useQueryClient();

  // The file is an admin archive carrying only its price part. The stream sends
  // the envelope first because exported_at is knowledge time and belongs to the
  // server's clock, not the browser's.
  async function handleExport() {
    setExportLoading(true);
    setExportError(null);
    try {
      let envelope: Envelope | undefined;
      const groups: PriceGroup[] = [];
      for await (const item of exportPrices()) {
        if (item.item.case === "envelope") {
          envelope = item.item.value;
        } else if (item.item.case === "group") {
          groups.push(item.item.value);
        }
      }
      if (!envelope) throw new Error("export stream sent no envelope");
      const json = marshalAdmin({ envelope, prices: { groups } });
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "prices.json";
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setExportError(err instanceof Error ? err.message : String(err));
    } finally {
      setExportLoading(false);
    }
  }

  return (
    <div className="space-y-4">
      {/* Header with export/import buttons */}
      <div className="flex flex-wrap items-end gap-3">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by instrument..."
          className="w-full max-w-sm rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
        />
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          From
          <input
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          To
          <input
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
          />
        </label>
        <div className="ml-auto flex gap-2">
          <button
            type="button"
            onClick={handleExport}
            disabled={exportLoading}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {exportLoading ? "Exporting..." : "Export archive"}
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

      {/* Keyed on the filters: a change remounts the list, which returns it to
          page 1. */}
      <PriceList
        key={`${debouncedSearch}|${dateFrom}|${dateTo}`}
        search={debouncedSearch}
        dateFrom={dateFrom}
        dateTo={dateTo}
      />

      <ImportPricesModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onComplete={() => queryClient.invalidateQueries({ queryKey: qk.prices() })}
      />
    </div>
  );
}

function PriceList({
  search,
  dateFrom,
  dateTo,
}: {
  search: string;
  dateFrom: string;
  dateTo: string;
}) {
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
  } = usePagination<EODPriceProto>({
    queryKey: qk.prices(search, dateFrom, dateTo),
    queryFn: async (pageToken) => {
      const result = await listPrices({
        search,
        dateFrom: dateFrom || undefined,
        // The picker is inclusive; the API bound is exclusive.
        dateBefore: dayAfter(dateTo),
        pageToken,
      });
      return {
        items: result.prices,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
  });

  return (
    <>
      {!loading && (
        <span className="block font-mono text-xs text-text-muted">{totalCount} total</span>
      )}
      {loading && <p className="text-text-muted">Loading prices...</p>}
      {!loading && error && <ErrorAlert>{errorMessage(error)}</ErrorAlert>}
      {!loading && !error && (
        <>
          <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-xs">
            <table data-testid="prices-table" className="w-full min-w-[700px] border-collapse text-sm">
              <thead>
                <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/3">
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
                      {search || dateFrom || dateTo
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

    </>
  );
}

function PriceRow({ price: p }: { price: EODPriceProto }) {
  return (
    <tr className="border-b border-border/40 last:border-0 hover:bg-primary-light/10">
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
        {p.close}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtPrice(p.adjustedClose)}
      </td>
      <td className="px-4 py-2 text-right font-mono text-text-muted">
        {fmtVolume(p.volume)}
      </td>
      <td className="px-4 py-2 text-text-muted">{p.dataProvider}</td>
    </tr>
  );
}

// --- Price Fetch Blocks Tab ---

function PriceFetchBlocksTab() {
  const queryClient = useQueryClient();
  const [clearError, setClearError] = useState<string | null>(null);
  const [clearing, setClearing] = useState<string | null>(null);

  const {
    data: blocks = [],
    isPending: loading,
    error: loadError,
  } = useAuthedQuery<PriceFetchBlock[]>({
    queryKey: qk.priceFetchBlocks(),
    queryFn: listPriceFetchBlocks,
  });

  const error =
    clearError ?? (loadError ? errorMessage(loadError, "Failed to load blocks") : null);

  async function handleClear(block: PriceFetchBlock) {
    const key = `${block.instrumentId}:${block.pluginId}`;
    setClearing(key);
    setClearError(null);
    try {
      await deletePriceFetchBlock(block.instrumentId, block.pluginId);
      // Drop the row from the cache rather than refetching: the delete already
      // tells us it is gone.
      queryClient.setQueryData<PriceFetchBlock[]>(qk.priceFetchBlocks(), (prev) =>
        (prev ?? []).filter(
          (b) => b.instrumentId !== block.instrumentId || b.pluginId !== block.pluginId
        )
      );
    } catch (e) {
      setClearError(errorMessage(e, "Failed to clear block"));
    } finally {
      setClearing(null);
    }
  }

  if (loading) {
    return <div className="text-text-muted">Loading price fetch blocks...</div>;
  }

  return (
    <div data-testid="fetch-blocks-tab" className="space-y-4">
      <p className="text-sm text-text-muted">
        Instruments blocked from specific price plugins due to permanent errors
        (e.g. HTTP 403, 404). Clear a block to allow the system to retry.
      </p>
      {error && <ErrorAlert>{error}</ErrorAlert>}
      {blocks.length === 0 ? (
        <p data-testid="fetch-blocks-empty" className="text-text-muted">No blocked instruments.</p>
      ) : (
        <div className="overflow-x-auto">
          <table data-testid="fetch-blocks-table" className="w-full text-sm">
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
                  <tr key={key} data-testid="fetch-block-row" className="border-b border-border/50">
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
                      {block.firstBlockedAt
                        ? timestampDate(block.firstBlockedAt).toLocaleDateString()
                        : ""}
                    </td>
                    <td className="py-2 text-right">
                      <button
                        data-testid="fetch-block-clear-btn"
                        type="button"
                        onClick={() => handleClear(block)}
                        disabled={clearing === key}
                        className="rounded-sm border border-border px-3 py-1 text-xs hover:bg-background disabled:opacity-50"
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
