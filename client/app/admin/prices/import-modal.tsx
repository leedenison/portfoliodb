"use client";

import { useEffect, useRef, useState } from "react";
import { Modal } from "@/app/components/modal";
import { ErrorAlert } from "@/app/components/error-alert";
import { csvToPrices } from "@/lib/csv/prices";
import { importPrices, getJob } from "@/lib/portfolio-api";
import { JobStatus } from "@/gen/api/v1/api_pb";
import type { PriceParseResult } from "@/lib/csv/prices";
import type { GetJobResult } from "@/lib/portfolio-api";

type Phase = "idle" | "preview" | "processing" | "result";

export function ImportPricesModal({
  open,
  onClose,
  onComplete,
}: {
  open: boolean;
  onClose: () => void;
  onComplete?: () => void;
}) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [parseResult, setParseResult] = useState<PriceParseResult | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [jobStatus, setJobStatus] = useState<GetJobResult | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [fileInputActive, setFileInputActive] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  function reset() {
    setPhase("idle");
    setParseResult(null);
    setJobId(null);
    setJobStatus(null);
    setImportError(null);
    setFile(null);
    setFileInputActive(false);
    if (fileRef.current) fileRef.current.value = "";
  }

  const processing = phase === "processing";

  function handleClose() {
    if (processing) return;
    reset();
    onClose();
  }

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    setFile(f);
    const file = f;
    const reader = new FileReader();
    reader.onload = (ev) => {
      const text = ev.target?.result as string;
      const result = csvToPrices(text);
      setParseResult(result);
      setPhase("preview");
    };
    reader.readAsText(file);
  }

  async function handleImport() {
    if (!parseResult) return;
    setPhase("processing");
    setImportError(null);
    try {
      const id = await importPrices(parseResult.prices, parseResult.exportedAt);
      setJobId(id);
    } catch (err) {
      setImportError(err instanceof Error ? err.message : String(err));
      setPhase("preview");
    }
  }

  // Poll job status.
  useEffect(() => {
    if (!jobId || phase !== "processing") return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await getJob(jobId);
        if (cancelled) return;
        setJobStatus(result);
        if (result.status === JobStatus.SUCCESS || result.status === JobStatus.FAILED) {
          setPhase("result");
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
  }, [jobId, phase]);

  function handleDone() {
    onComplete?.();
    reset();
    onClose();
  }

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Import prices"
      closable={!processing}
    >
      <div className="flex flex-col gap-4 overflow-y-auto p-5">
        {phase === "idle" && (
          <div className="space-y-3">
            <p className="text-sm text-text-muted">
              Select a CSV file to import prices.
            </p>
            <input
              ref={fileRef}
              type="file"
              accept=".csv"
              onChange={handleFileChange}
              className="sr-only"
              aria-label="Choose CSV file"
            />
            <button
              type="button"
              onClick={() => {
                setFileInputActive(true);
                fileRef.current?.click();
                setTimeout(() => setFileInputActive(false), 400);
              }}
              className={`rounded-md border px-4 py-2 text-sm font-semibold transition-colors ${
                fileInputActive
                  ? "border-primary bg-primary text-white"
                  : "border-border bg-primary-light/20 text-text-primary hover:bg-primary-light/40 active:border-primary active:bg-primary active:text-white"
              }`}
            >
              {fileInputActive ? "Opening\u2026" : "Choose file"}
            </button>
            {file && (
              <p className="text-sm text-text-muted">
                Selected: {file.name}
              </p>
            )}
          </div>
        )}

        {phase === "preview" && parseResult && (
          <div className="space-y-4">
            {parseResult.errors.length > 0 ? (
              <div className="space-y-2">
                <p className="text-sm font-medium text-text-primary">
                  Parse errors ({parseResult.errors.length})
                </p>
                <div className="max-h-48 overflow-y-auto rounded-md border border-border bg-surface">
                  <table className="w-full border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-border bg-primary-dark/[0.03]">
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Row</th>
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Field</th>
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Error</th>
                      </tr>
                    </thead>
                    <tbody>
                      {parseResult.errors.map((e, i) => (
                        <tr key={i} className="border-b border-border/40 last:border-0">
                          <td className="px-3 py-1.5 font-mono text-text-muted">{e.rowIndex}</td>
                          <td className="px-3 py-1.5 font-mono text-text-primary">{e.field}</td>
                          <td className="px-3 py-1.5 text-accent-dark">{e.message}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {parseResult.prices.length > 0 && (
                  <p className="text-xs text-text-muted">
                    {parseResult.prices.length} price{parseResult.prices.length !== 1 ? "s" : ""} parsed successfully (with errors above).
                  </p>
                )}
              </div>
            ) : (
              <p className="text-sm text-text-primary">
                Ready to import{" "}
                <span className="font-semibold">{parseResult.prices.length}</span>{" "}
                price{parseResult.prices.length !== 1 ? "s" : ""}.
              </p>
            )}

            {importError && <ErrorAlert>{importError}</ErrorAlert>}

            <div className="flex gap-2">
              <button
                type="button"
                onClick={handleImport}
                disabled={parseResult.prices.length === 0}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark disabled:cursor-not-allowed disabled:opacity-50"
              >
                Import
              </button>
              <button
                type="button"
                onClick={reset}
                className="rounded-md border border-border px-4 py-2 text-sm font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:opacity-50"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        {phase === "processing" && (
          <div className="flex flex-col items-center gap-3 py-6">
            <svg
              className="h-8 w-8 animate-spin text-primary"
              viewBox="0 0 24 24"
              fill="none"
            >
              <circle
                className="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                strokeWidth="3"
              />
              <path
                className="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8v3a5 5 0 00-5 5H4z"
              />
            </svg>
            <p className="text-sm text-text-muted">
              {jobStatus && jobStatus.totalCount > 0
                ? `Processed ${jobStatus.processedCount.toLocaleString()} of ${jobStatus.totalCount.toLocaleString()} prices\u2026`
                : "Processing\u2026"}
            </p>
          </div>
        )}

        {phase === "result" && jobStatus && (
          <div className="space-y-4">
            {jobStatus.status === JobStatus.SUCCESS ? (
              <p className="text-sm text-text-primary">
                Import complete:{" "}
                <span className="font-semibold">{jobStatus.processedCount.toLocaleString()}</span>{" "}
                price{jobStatus.processedCount !== 1 ? "s" : ""} processed.
              </p>
            ) : (
              <p className="text-sm text-accent-dark font-medium">
                Import failed.
              </p>
            )}

            {jobStatus.validationErrors.length > 0 && (
              <div className="space-y-2">
                <p className="text-sm font-medium text-accent-dark">
                  {jobStatus.validationErrors.length} error{jobStatus.validationErrors.length !== 1 ? "s" : ""}
                </p>
                <div className="max-h-48 overflow-y-auto rounded-md border border-border bg-surface">
                  <table className="w-full border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-border bg-primary-dark/[0.03]">
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Row</th>
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Field</th>
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Error</th>
                      </tr>
                    </thead>
                    <tbody>
                      {jobStatus.validationErrors.map((e, i) => (
                        <tr key={i} className="border-b border-border/40 last:border-0">
                          <td className="px-3 py-1.5 font-mono text-text-muted">{e.rowIndex}</td>
                          <td className="px-3 py-1.5 font-mono text-text-primary">{e.field}</td>
                          <td className="px-3 py-1.5 text-accent-dark">{e.message}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}

            <button
              type="button"
              onClick={handleDone}
              className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark"
            >
              Done
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
}
