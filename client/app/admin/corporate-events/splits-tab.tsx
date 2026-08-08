"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import { exportCorporateEvents } from "@/lib/portfolio-api";
import { marshalSystem } from "@/lib/archive/codec";
import type { Envelope } from "@/gen/archive/v1/common_pb";
import type { CorporateEventGroup } from "@/gen/archive/v1/corporate_events_pb";
import { ImportCorporateEventsModal } from "./import-modal";

interface SplitDisplay {
  identifierValue: string;
  identifierDomain: string;
  exDate: string;
  splitFrom: string;
  splitTo: string;
}

export function SplitsTab() {
  const queryClient = useQueryClient();
  const [exportLoading, setExportLoading] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);

  // The export is a streaming generator, so the queryFn drains it and flattens
  // the groups back out. This tab lists splits; the dividends the same file
  // carries have a tab of their own that is not built yet.
  const {
    data: splits = [],
    isPending: loading,
    error: loadError,
  } = useAuthedQuery<SplitDisplay[]>({
    queryKey: qk.corporateEventSplits(),
    queryFn: async () => {
      const rows: SplitDisplay[] = [];
      for await (const item of exportCorporateEvents()) {
        if (item.item.case !== "group") continue;
        const group = item.item.value;
        for (const event of group.events) {
          if (event.event.case !== "split") continue;
          rows.push({
            identifierValue: group.instrument?.value ?? "",
            identifierDomain: group.instrument?.domain ?? "",
            exDate: event.event.value.exDate,
            splitFrom: event.event.value.splitFrom,
            splitTo: event.event.value.splitTo,
          });
        }
      }
      rows.sort((a, b) => b.exDate.localeCompare(a.exDate));
      return rows;
    },
  });

  const loadSplits = () => queryClient.invalidateQueries({ queryKey: qk.corporateEventSplits() });

  const error = exportError ?? (loadError ? errorMessage(loadError, "Failed to load splits") : null);

  // The file is an admin archive carrying only its corporate event part. The
  // stream sends the envelope first because exported_at is knowledge time and
  // belongs to the server's clock, not the browser's.
  async function handleExport() {
    setExportLoading(true);
    setExportError(null);
    try {
      let envelope: Envelope | undefined;
      const groups: CorporateEventGroup[] = [];
      for await (const item of exportCorporateEvents()) {
        if (item.item.case === "envelope") {
          envelope = item.item.value;
        } else if (item.item.case === "group") {
          groups.push(item.item.value);
        }
      }
      if (!envelope) throw new Error("export stream sent no envelope");
      const json = marshalSystem({ envelope, corporateEvents: { groups } });
      const blob = new Blob([json], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "corporate-events.json";
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
            {exportLoading ? "Exporting..." : "Export archive"}
          </button>
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15"
          >
            Import archive
          </button>
        </div>
      </div>

      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      <ImportCorporateEventsModal
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
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
