"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import type { ExportCorporateEventRow } from "@/gen/api/v1/api_pb";
import {
  exportCorporateEvents,
  importCorporateEventSplits,
  getJob,
  type GetJobResult,
} from "@/lib/portfolio-api";
import { JobStatus } from "@/gen/api/v1/api_pb";
import { splitsToJson, parseSplitsJson } from "@/lib/json/corporate-events";
import { ImportCorporateEventsModal } from "./import-modal";

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
  const [importJsonError, setImportJsonError] = useState<string | null>(null);
  const [importJobId, setImportJobId] = useState<string | null>(null);
  const [importJobStatus, setImportJobStatus] = useState<GetJobResult | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

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

  function handleJsonFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    setImportJsonError(null);
    const reader = new FileReader();
    reader.onload = async (ev) => {
      const text = (ev.target?.result as string) ?? "";
      const result = parseSplitsJson(text);
      if (result.errors.length > 0) {
        setImportJsonError(result.errors.map((e) => `Row ${e.rowIndex}: ${e.field} - ${e.message}`).join("\n"));
        if (fileRef.current) fileRef.current.value = "";
        return;
      }
      if (result.splits.length === 0) {
        setImportJsonError("No splits found in file.");
        if (fileRef.current) fileRef.current.value = "";
        return;
      }
      try {
        const jobId = await importCorporateEventSplits(result.splits);
        setImportJobId(jobId);
      } catch (err) {
        setImportJsonError(err instanceof Error ? err.message : String(err));
      }
      if (fileRef.current) fileRef.current.value = "";
    };
    reader.readAsText(f);
  }

  // Poll JSON import job.
  useEffect(() => {
    if (!importJobId) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await getJob(importJobId);
        if (cancelled) return;
        setImportJobStatus(result);
        if (result.status === JobStatus.SUCCESS || result.status === JobStatus.FAILED) {
          if (result.status === JobStatus.SUCCESS) loadSplits();
          setImportJobId(null);
        }
      } catch {
        // Ignore transient poll errors.
      }
    };
    poll();
    const t = setInterval(poll, 2000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [importJobId, loadSplits]);

  function dismissJobStatus() {
    setImportJobStatus(null);
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
          <label className="cursor-pointer rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15">
            Import JSON
            <input
              ref={fileRef}
              type="file"
              accept=".json"
              onChange={handleJsonFileChange}
              className="sr-only"
            />
          </label>
          <button
            type="button"
            onClick={() => setImportOpen(true)}
            className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15"
          >
            Import from Broker
          </button>
        </div>
      </div>

      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      {importJsonError && (
        <div className="mt-2">
          <ErrorAlert>{importJsonError}</ErrorAlert>
        </div>
      )}

      {importJobId && (
        <p className="mt-2 text-sm text-text-muted">
          Importing...{importJobStatus && importJobStatus.totalCount > 0
            ? ` ${importJobStatus.processedCount} of ${importJobStatus.totalCount}`
            : ""}
        </p>
      )}

      {importJobStatus && !importJobId && (
        <div className="mt-2 flex items-center gap-2 text-sm">
          {importJobStatus.status === JobStatus.SUCCESS ? (
            <span className="text-text-primary">
              Import complete: {importJobStatus.processedCount} event{importJobStatus.processedCount !== 1 ? "s" : ""} processed.
            </span>
          ) : (
            <span className="text-accent-dark">Import failed.</span>
          )}
          <button
            type="button"
            onClick={dismissJobStatus}
            className="text-xs text-text-muted underline hover:text-text-primary"
          >
            Dismiss
          </button>
        </div>
      )}

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

      <ImportCorporateEventsModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onComplete={() => loadSplits()}
      />
    </div>
  );
}
