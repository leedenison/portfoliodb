"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { exportCorporateEvents } from "@/lib/portfolio-api";
import { splitsToJson } from "@/lib/json/corporate-events";
import type { ExportCorporateEventRow } from "@/gen/api/v1/api_pb";
import { ImportSplitsModal } from "./import-splits-modal";

interface SplitDisplay {
  identifierType: string;
  identifierValue: string;
  identifierDomain: string;
  exDate: string;
  splitFrom: string;
  splitTo: string;
  dataProvider: string;
}

export function SplitsTab() {
  const [splits, setSplits] = useState<SplitDisplay[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [exportLoading, setExportLoading] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const loadSplits = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const rows: SplitDisplay[] = [];
      for await (const row of exportCorporateEvents()) {
        if (row.event.case === "split") {
          rows.push({
            identifierType: row.identifierType,
            identifierValue: row.identifierValue,
            identifierDomain: row.identifierDomain,
            exDate: row.event.value.exDate,
            splitFrom: row.event.value.splitFrom,
            splitTo: row.event.value.splitTo,
            dataProvider: row.dataProvider,
          });
        }
      }
      rows.sort((a, b) => b.exDate.localeCompare(a.exDate));
      setSplits(rows);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load splits");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSplits();
  }, [loadSplits]);

  async function handleExport() {
    setExportLoading(true);
    setError(null);
    try {
      const rows: ExportCorporateEventRow[] = [];
      for await (const row of exportCorporateEvents()) {
        if (row.event.case === "split") rows.push(row);
      }
      const json = splitsToJson(rows);
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "splits.json";
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Export failed");
    } finally {
      setExportLoading(false);
    }
  }

  return (
    <div className="mt-4 space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="ml-auto flex gap-2">
          <button
            type="button"
            onClick={handleExport}
            disabled={exportLoading}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {exportLoading ? "Exporting..." : "Export JSON"}
          </button>
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15"
          >
            Import JSON
          </button>
        </div>
      </div>

      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      <ImportSplitsModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onComplete={loadSplits}
      />

      {loading ? (
        <p className="mt-4 text-text-muted">Loading splits...</p>
      ) : splits.length === 0 ? (
        <p className="mt-4 text-text-muted">No stock splits.</p>
      ) : (
        <table className="mt-4 w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border text-text-muted">
              <th className="py-2 pr-4 font-medium">Instrument</th>
              <th className="py-2 pr-4 font-medium">Ex Date</th>
              <th className="py-2 pr-4 font-medium">From</th>
              <th className="py-2 pr-4 font-medium">To</th>
              <th className="py-2 pr-4 font-medium">Provider</th>
            </tr>
          </thead>
          <tbody>
            {splits.map((s) => (
              <tr key={`${s.identifierValue}-${s.exDate}`} className="border-b border-border">
                <td className="py-2 pr-4 font-mono text-text-primary">
                  {s.identifierDomain ? `${s.identifierDomain}:` : ""}
                  {s.identifierValue}
                </td>
                <td className="py-2 pr-4 text-text-muted">{s.exDate}</td>
                <td className="py-2 pr-4 text-text-muted">{s.splitFrom}</td>
                <td className="py-2 pr-4 text-text-muted">{s.splitTo}</td>
                <td className="py-2 pr-4 text-text-muted">{s.dataProvider}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
