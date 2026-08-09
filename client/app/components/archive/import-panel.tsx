"use client";

import { useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { archiveErrorMessage } from "@/lib/archive/codec";
import { errorMessage } from "@/lib/errors";

/**
 * Upload one archive, of either kind.
 *
 * The file is parsed here so that a file that is not an archive at all -- or is
 * the other kind of archive -- is refused before anything is queued: whether a
 * file is a valid archive of this kind is an all-or-nothing question answered at
 * parse time. Whether its contents resolve against this instance is a separate,
 * per-part question the job answers.
 *
 * Both pages share this rather than each writing it, because the sequence of
 * states is the behaviour, not the decoration: parse before queue, say what the
 * file carries, and refuse to start a second import over a running one.
 */
export function ImportArchivePanel<T>({
  running,
  onStarted,
  parse,
  counts,
  submit,
  note,
}: {
  running: boolean;
  onStarted: (jobId: string) => void;
  /** Parse the file's text into a document, throwing if it is not one. */
  parse: (text: string) => T;
  /** What the parsed document carries, for the preview line. */
  counts: (archive: T) => { label: string; count: number }[];
  /** Queue the document, returning the job to poll. */
  submit: (archive: T, filename: string) => Promise<string>;
  /** What this page's import does, in a sentence or two. */
  note: React.ReactNode;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [archive, setArchive] = useState<T | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [fileInputActive, setFileInputActive] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  function reset() {
    setFile(null);
    setArchive(null);
    setParseError(null);
    setSubmitError(null);
    if (fileRef.current) fileRef.current.value = "";
  }

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    setFile(f);
    setSubmitError(null);
    const reader = new FileReader();
    reader.onload = (ev) => {
      try {
        setArchive(parse(ev.target?.result as string));
        setParseError(null);
      } catch (err) {
        setArchive(null);
        setParseError(archiveErrorMessage(err));
      }
    };
    reader.readAsText(f);
  }

  async function handleImport() {
    if (!archive || !file) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const jobId = await submit(archive, file.name);
      onStarted(jobId);
      reset();
    } catch (e) {
      setSubmitError(errorMessage(e, "Import failed"));
    } finally {
      setSubmitting(false);
    }
  }

  const partCounts = archive ? counts(archive) : [];

  return (
    <section className="rounded-lg border border-border bg-surface p-4" data-testid="archive-import">
      <h2 className="font-display text-base font-semibold text-text-primary">Import</h2>
      <div className="mt-1 text-sm text-text-muted">{note}</div>

      <div className="mt-3 space-y-3">
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
          disabled={running || submitting}
          onClick={() => {
            setFileInputActive(true);
            fileRef.current?.click();
            setTimeout(() => setFileInputActive(false), 400);
          }}
          data-testid="choose-archive-file"
          className={`rounded-md border px-4 py-2 text-sm font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
            fileInputActive
              ? "border-primary bg-primary text-white"
              : "border-border bg-primary-light/20 text-text-primary hover:bg-primary-light/40 active:border-primary active:bg-primary active:text-white"
          }`}
        >
          {fileInputActive ? "Opening…" : "Choose file"}
        </button>

        {file && <p className="text-sm text-text-muted">Selected: {file.name}</p>}
        {parseError && <ErrorAlert>{parseError}</ErrorAlert>}
        {submitError && <ErrorAlert>{submitError}</ErrorAlert>}

        {archive && !parseError && (
          <>
            <p className="text-sm text-text-primary">
              {partCounts.length === 0
                ? "This archive carries no parts."
                : `Carries ${partCounts.map((c) => `${c.count.toLocaleString()} ${c.label}`).join(", ")}.`}
            </p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={handleImport}
                disabled={submitting || running || partCounts.length === 0}
                data-testid="start-archive-import"
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting ? "Starting…" : "Import"}
              </button>
              <button
                type="button"
                onClick={reset}
                className="rounded-md border border-border px-4 py-2 text-sm font-medium text-text-primary transition-colors hover:bg-primary-light/15"
              >
                Cancel
              </button>
            </div>
          </>
        )}
        {running && (
          <p className="text-sm text-text-muted">An import is already running.</p>
        )}
      </div>
    </section>
  );
}
