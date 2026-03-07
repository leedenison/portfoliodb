"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { getJob } from "@/lib/portfolio-api";
import { upsertTxs } from "@/lib/ingestion-api";
import { parseStandardCSV } from "@/lib/csv/standard";
import { Broker } from "@/gen/api/v1/api_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { JobStatus } from "@/gen/api/v1/api_pb";
import {
  getBrokerOptionsForUpload,
  getFormatsForBroker,
  getSourcePrefix,
} from "@/lib/csv/converters";

const BROKER_OPTIONS = getBrokerOptionsForUpload();
const DEFAULT_BROKER = BROKER_OPTIONS[0]?.value ?? Broker.FIDELITY;

export default function UploadPage() {
  const { state, authError } = useAuth();
  const [step, setStep] = useState<1 | 2>(1);
  const [broker, setBroker] = useState<Broker>(DEFAULT_BROKER);
  const [formatId, setFormatId] = useState<string>("standard");
  const [converterOptions, setConverterOptions] = useState<Record<string, unknown>>({});
  const [file, setFile] = useState<File | null>(null);
  const [parseResult, setParseResult] = useState<ReturnType<typeof parseStandardCSV> | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [jobStatus, setJobStatus] = useState<Awaited<ReturnType<typeof getJob>> | null>(null);
  const [fileInputActive, setFileInputActive] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const formats = useMemo(() => getFormatsForBroker(broker), [broker]);
  const selectedFormat = useMemo(() => formats.find((f) => f.id === formatId), [formats, formatId]);
  const optionsValid =
    selectedFormat?.OptionsComponent == null ||
    (converterOptions?.currency != null && converterOptions?.currency !== "");

  const handleFileChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const f = e.target.files?.[0];
      setFile(f ?? null);
      setParseResult(null);
      setSubmitError(null);
      if (!f) return;
      const reader = new FileReader();
      reader.onload = () => {
        const text = typeof reader.result === "string" ? reader.result : "";
        if (selectedFormat?.convert) {
          const result = selectedFormat.convert!(text, converterOptions);
          setParseResult(result);
        } else {
          setParseResult(parseStandardCSV(text));
        }
      };
      reader.readAsText(f);
    },
    [selectedFormat, converterOptions]
  );

  const handleUpload = useCallback(async () => {
    if (!parseResult || parseResult.errors.length > 0 || parseResult.txs.length === 0) return;
    setSubmitError(null);
    try {
      const sourcePrefix = getSourcePrefix(broker);
      const source = `${sourcePrefix}:web:${formatId}`;
      const res = await upsertTxs({
        broker,
        source,
        periodFrom: timestampFromDate(parseResult.periodFrom),
        periodTo: timestampFromDate(parseResult.periodTo),
        txs: parseResult.txs,
      });
      setJobId(res.jobId);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : String(e));
    }
  }, [broker, formatId, parseResult]);

  useEffect(() => {
    setParseResult(null);
  }, [broker, formatId, converterOptions]);

  // Re-parse when format or converter options change and we already have a file
  // (e.g. user picked file with "standard", then switched to "Fidelity CSV" and selected currency)
  useEffect(() => {
    if (!file || !selectedFormat || !optionsValid) return;
    const reader = new FileReader();
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      if (selectedFormat.convert) {
        const result = selectedFormat.convert(text, converterOptions);
        setParseResult(result);
      } else {
        setParseResult(parseStandardCSV(text));
      }
    };
    reader.readAsText(file);
  }, [file, formatId, selectedFormat, converterOptions, optionsValid]);

  useEffect(() => {
    if (!jobId || state.status !== "authenticated") return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await getJob(jobId);
        if (!cancelled) setJobStatus(result);
        return result.status === JobStatus.SUCCESS || result.status === JobStatus.FAILED;
      } catch {
        return false;
      }
    };
    poll();
    const t = setInterval(() => {
      poll().then((done) => {
        if (done) clearInterval(t);
      });
    }, 2000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [jobId, state.status]);

  if (state.status === "loading") {
    return (
      <AppShell>
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-text-muted">Loading…</p>
        </div>
      </AppShell>
    );
  }

  if (state.status === "unauthenticated") {
    return (
      <AppShell>
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8 text-center">
          <h1 className="text-4xl font-bold tracking-tight text-text-primary">Upload transactions</h1>
          <p className="mt-3 text-text-muted">Sign in to upload.</p>
          {authError && (
            <p className="mt-4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">{authError}</p>
          )}
        </div>
      </AppShell>
    );
  }

  const canUpload =
    parseResult &&
    parseResult.errors.length === 0 &&
    parseResult.txs.length > 0 &&
    optionsValid &&
    !jobId;

  return (
    <AppShell>
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        <div className="w-full max-w-2xl space-y-4">
          <Link
            href="/holdings"
            className="text-sm text-text-muted underline transition-colors hover:text-primary"
          >
            Back to holdings
          </Link>
          <h2 className="text-xl font-semibold text-text-primary">
            Upload transactions
          </h2>

          {jobId && jobStatus && (jobStatus.status === JobStatus.SUCCESS || jobStatus.status === JobStatus.FAILED) ? (
            <div className="space-y-3 rounded-lg border border-border bg-surface p-4 shadow-sm">
              {jobStatus.status === JobStatus.SUCCESS ? (
                <>
                  <p className="text-text-primary">Upload completed successfully.</p>
                  <Link
                    href="/holdings"
                    className="inline-block text-sm text-primary underline hover:text-primary-dark"
                  >
                    View holdings
                  </Link>
                </>
              ) : (
                <>
                  <p className="font-medium text-accent-dark">Upload failed</p>
                  {jobStatus.validationErrors.length > 0 && (
                    <div>
                      <p className="text-sm font-medium text-text-primary">Validation errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                        {jobStatus.validationErrors.map((e, i) => (
                          <li key={i}>
                            Row {e.rowIndex + 1}: {e.field} – {e.message}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {jobStatus.identificationErrors.length > 0 && (
                    <div>
                      <p className="text-sm font-medium text-text-primary">Identification errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                        {jobStatus.identificationErrors.map((e, i) => (
                          <li key={i}>
                            Row {e.rowIndex + 1}: {e.instrumentDescription} – {e.message}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </>
              )}
            </div>
          ) : jobId ? (
            <p className="text-text-muted">Processing…</p>
          ) : step === 1 ? (
            <>
              <p className="text-text-muted">Select the broker for this transaction file.</p>
              <div className="space-y-2">
                <label htmlFor="broker" className="block text-sm font-medium text-text-primary">
                  Broker
                </label>
                <select
                  id="broker"
                  value={broker}
                  onChange={(e) => {
                    setBroker(Number(e.target.value) as Broker);
                    setFormatId("standard");
                    setConverterOptions({});
                  }}
                  className="block w-full rounded-lg border border-border bg-surface px-3 py-2 text-text-primary"
                >
                  {BROKER_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
              </div>
              <button
                type="button"
                onClick={() => setStep(2)}
                className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark"
              >
                Next
              </button>
            </>
          ) : (
            <>
              <p className="text-text-muted">
                Choose format and select your CSV file.
              </p>
              <div className="space-y-2">
                <label htmlFor="format" className="block text-sm font-medium text-text-primary">
                  Format
                </label>
                <select
                  id="format"
                  value={formatId}
                  onChange={(e) => setFormatId(e.target.value)}
                  className="block w-full rounded-lg border border-border bg-surface px-3 py-2 text-text-primary"
                >
                  {formats.map((f) => (
                    <option key={f.id} value={f.id}>
                      {f.label}
                    </option>
                  ))}
                </select>
              </div>
              {selectedFormat?.OptionsComponent && (() => {
                const OptionsComponent = selectedFormat.OptionsComponent;
                return (
                  <div className="space-y-2">
                    <OptionsComponent
                      onOptionsChange={setConverterOptions}
                      options={converterOptions}
                    />
                  </div>
                );
              })()}
              <div className="space-y-2">
                <label htmlFor="file" className="block text-sm font-medium text-text-primary">
                  CSV file
                </label>
                <input
                  ref={fileInputRef}
                  id="file"
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
                    fileInputRef.current?.click();
                    setTimeout(() => setFileInputActive(false), 400);
                  }}
                  className={`rounded-lg border px-4 py-2 text-sm font-medium transition-colors ${
                    fileInputActive
                      ? "border-primary bg-primary text-white"
                      : "border-border bg-primary-light/30 text-text-primary hover:bg-primary-light/50 active:border-primary active:bg-primary active:text-white"
                  }`}
                >
                  {fileInputActive ? "Opening…" : "Choose file"}
                </button>
                {file && (
                  <p className="text-sm text-text-muted">
                    Selected: {file.name}
                  </p>
                )}
              </div>
              {parseResult && (
                <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
                  {parseResult.errors.length > 0 ? (
                    <>
                      <p className="font-medium text-accent-dark">Parse errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                        {parseResult.errors.map((e, i) => (
                          <li key={i}>
                            Row {e.rowIndex}: {e.field} – {e.message}
                          </li>
                        ))}
                      </ul>
                    </>
                  ) : (
                    <>
                      <p className="text-text-primary">
                        {parseResult.txs.length} transaction(s), from{" "}
                        {parseResult.periodFrom.toLocaleDateString()} to{" "}
                        {parseResult.periodTo.toLocaleDateString()}.
                      </p>
                      <button
                        type="button"
                        onClick={handleUpload}
                        disabled={!canUpload}
                        className="mt-3 rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-accent-dark disabled:opacity-50"
                      >
                        Upload
                      </button>
                    </>
                  )}
                </div>
              )}
              {submitError && (
                <p className="rounded-lg bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">{submitError}</p>
              )}
              <button
                type="button"
                onClick={() => setStep(1)}
                className="text-sm text-text-muted underline hover:text-primary"
              >
                Back
              </button>
            </>
          )}
        </div>
      </div>
    </AppShell>
  );
}
