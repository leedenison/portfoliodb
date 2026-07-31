"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import { exportCorporateEvents } from "@/lib/portfolio-api";
import { splitsToJson } from "@/lib/json/corporate-events";
import type { ExportCorporateEventRow, ExportCoverage } from "@/gen/api/v1/api_pb";
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
  const queryClient = useQueryClient();
  const [exportLoading, setExportLoading] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);

  // The export is a streaming generator, so the queryFn drains it and returns
  // the accumulated rows. The generator itself is unchanged.
  const {
    data: splits = [],
    isPending: loading,
    error: loadError,
  } = useAuthedQuery<SplitDisplay[]>({
    queryKey: qk.corporateEventSplits(),
    queryFn: async () => {
      const rows: SplitDisplay[] = [];
      for await (const item of exportCorporateEvents()) {
        if (item.item.case !== "row") continue;
        const row = item.item.value;
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
      return rows;
    },
  });

  const loadSplits = () => queryClient.invalidateQueries({ queryKey: qk.corporateEventSplits() });

  const error = exportError ?? (loadError ? errorMessage(loadError, "Failed to load splits") : null);

  async function handleExport() {
    setExportLoading(true);
    setExportError(null);
    try {
      const rows: ExportCorporateEventRow[] = [];
      const coverage: ExportCoverage[] = [];
      for await (const item of exportCorporateEvents()) {
        if (item.item.case === "coverage") {
          coverage.push(item.item.value);
        } else if (item.item.case === "row" && item.item.value.event.case === "split") {
          rows.push(item.item.value);
        }
      }
      const json = splitsToJson(rows, coverage);
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "splits.json";
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setExportError(errorMessage(e, "Export failed"));
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
