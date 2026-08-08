"use client";

import { useRef, useState } from "react";
import { Modal } from "@/app/components/modal";
import { ErrorAlert } from "@/app/components/error-alert";
import { archiveErrorMessage, unmarshalSystem } from "@/lib/archive/codec";
import { importInstruments } from "@/lib/portfolio-api";
import type { SystemArchive } from "@/gen/archive/v1/archive_pb";
import type { ImportInstrumentsResult } from "@/lib/portfolio-api";

type Phase = "idle" | "preview" | "result";

export function ImportInstrumentsModal({
  open,
  onClose,
  onComplete,
}: {
  open: boolean;
  onClose: () => void;
  onComplete?: () => void;
}) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [archive, setArchive] = useState<SystemArchive | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<ImportInstrumentsResult | null>(null);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [fileInputActive, setFileInputActive] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  function reset() {
    setPhase("idle");
    setArchive(null);
    setParseError(null);
    setImportResult(null);
    setImporting(false);
    setImportError(null);
    setFile(null);
    if (fileRef.current) fileRef.current.value = "";
  }

  function handleClose() {
    if (importing) return;
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
      try {
        const parsed = unmarshalSystem(text);
        // Only the instrument part is applied. A file carrying the other
        // sections is still a valid archive; each has its own importer.
        setArchive(parsed);
        setParseError(null);
      } catch (err) {
        setArchive(null);
        setParseError(archiveErrorMessage(err));
      }
      setPhase("preview");
    };
    reader.readAsText(file);
  }

  async function handleImport() {
    if (!archive?.envelope || !archive.instruments) return;
    setImporting(true);
    setImportError(null);
    try {
      const result = await importInstruments(archive.envelope, archive.instruments);
      setImportResult(result);
      setPhase("result");
    } catch (err) {
      setImportError(err instanceof Error ? err.message : String(err));
    } finally {
      setImporting(false);
    }
  }

  const instruments = archive?.instruments?.instruments ?? [];

  function handleDone() {
    onComplete?.();
    reset();
    onClose();
  }

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Import instruments"
      closable={!importing}
    >
      <div className="flex flex-col gap-4 overflow-y-auto p-5">
        {phase === "idle" && (
          <div className="space-y-3">
            <p className="text-sm text-text-muted">
              Select an archive file to import instruments.
            </p>
            <input
              ref={fileRef}
              type="file"
              accept=".json,application/json"
              onChange={handleFileChange}
              className="sr-only"
              aria-label="Choose archive file"
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

        {phase === "preview" && (
          <div className="space-y-4">
            {/* Whether the file is a valid archive is settled here, all or
                nothing. Whether an underlying reference resolves against this
                instance is a separate question the response answers per index. */}
            {parseError ? (
              <ErrorAlert>{parseError}</ErrorAlert>
            ) : (
              <p className="text-sm text-text-primary">
                Ready to import{" "}
                <span className="font-semibold">{instruments.length.toLocaleString()}</span>{" "}
                instrument{instruments.length !== 1 ? "s" : ""}.
              </p>
            )}

            {importError && <ErrorAlert>{importError}</ErrorAlert>}

            <div className="flex gap-2">
              <button
                type="button"
                onClick={handleImport}
                disabled={importing || instruments.length === 0}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark disabled:cursor-not-allowed disabled:opacity-50"
              >
                {importing ? "Importing..." : "Import"}
              </button>
              <button
                type="button"
                onClick={reset}
                disabled={importing}
                className="rounded-md border border-border px-4 py-2 text-sm font-medium text-text-primary transition-colors hover:bg-primary-light/15 disabled:opacity-50"
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        {phase === "result" && importResult && (
          <div className="space-y-4">
            <p className="text-sm text-text-primary">
              Import complete:{" "}
              <span className="font-semibold">{importResult.ensuredCount}</span>{" "}
              instrument{importResult.ensuredCount !== 1 ? "s" : ""} ensured.
            </p>

            {importResult.errors.length > 0 && (
              <div className="space-y-2">
                <p className="text-sm font-medium text-accent-dark">
                  {importResult.errors.length} error{importResult.errors.length !== 1 ? "s" : ""}
                </p>
                <div className="max-h-48 overflow-y-auto rounded-md border border-border bg-surface">
                  <table className="w-full border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-border bg-primary-dark/3">
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Index</th>
                        <th className="px-3 py-2 text-left font-semibold uppercase tracking-wider text-text-muted">Error</th>
                      </tr>
                    </thead>
                    <tbody>
                      {importResult.errors.map((e, i) => (
                        <tr key={i} className="border-b border-border/40 last:border-0">
                          <td className="px-3 py-1.5 font-mono text-text-muted">{e.index}</td>
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
