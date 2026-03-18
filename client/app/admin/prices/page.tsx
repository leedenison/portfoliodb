"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  listPriceFetchBlocks,
  deletePriceFetchBlock,
} from "@/lib/portfolio-api";
import type { PriceFetchBlock } from "@/gen/api/v1/api_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";

export default function AdminPricesPage() {
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
    <div className="space-y-6">
      <h1 className="font-display text-xl font-bold text-text-primary">
        Price fetch blocks
      </h1>
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
                    <td className="py-2 pr-4 text-text-muted">{block.reason}</td>
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
