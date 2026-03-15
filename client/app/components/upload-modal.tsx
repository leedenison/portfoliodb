"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { Modal } from "@/app/components/modal";
import { useUploadModal } from "@/contexts/upload-modal-context";
import { useAuth } from "@/contexts/auth-context";
import { getJob } from "@/lib/portfolio-api";
import { upsertTxs } from "@/lib/ingestion-api";
import { parseStandardCSV } from "@/lib/csv/standard";
import { Broker, JobStatus } from "@/gen/api/v1/api_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  getBrokerOptionsForUpload,
  getFormatsForBroker,
  getSourcePrefix,
} from "@/lib/csv/converters";

const BROKER_OPTIONS = getBrokerOptionsForUpload();
const DEFAULT_BROKER = BROKER_OPTIONS[0]?.value ?? Broker.FIDELITY;

export function UploadModal() {
  const { isOpen, closeUploadModal, onComplete } = useUploadModal();
  const { state } = useAuth();
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

  // Reset state when modal opens.
  useEffect(() => {
    if (isOpen) {
      setStep(1);
      setBroker(DEFAULT_BROKER);
      setFormatId("standard");
      setConverterOptions({});
      setFile(null);
      setParseResult(null);
      setSubmitError(null);
      setJobId(null);
      setJobStatus(null);
    }
  }, [isOpen]);

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
          setParseResult(selectedFormat.convert(text, converterOptions));
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
        filename: file?.name,
      });
      setJobId(res.jobId);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : String(e));
    }
  }, [broker, formatId, parseResult, file]);

  // Clear parse result when broker/format/options change.
  useEffect(() => {
    setParseResult(null);
  }, [broker, formatId, converterOptions]);

  // Re-parse when format or converter options change and we already have a file.
  useEffect(() => {
    if (!file || !selectedFormat || !optionsValid) return;
    const reader = new FileReader();
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      if (selectedFormat.convert) {
        setParseResult(selectedFormat.convert(text, converterOptions));
      } else {
        setParseResult(parseStandardCSV(text));
      }
    };
    reader.readAsText(file);
  }, [file, formatId, selectedFormat, converterOptions, optionsValid]);

  // Poll job status; auto-close on success.
  useEffect(() => {
    if (!jobId || state.status !== "authenticated") return;
    let cancelled = false;
    const poll = async () => {
      try {
        const result = await getJob(jobId);
        if (cancelled) return false;
        setJobStatus(result);
        if (result.status === JobStatus.SUCCESS) {
          onComplete?.();
          closeUploadModal();
          return true;
        }
        return result.status === JobStatus.FAILED;
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
  }, [jobId, state.status, onComplete, closeUploadModal]);

  const canUpload =
    parseResult &&
    parseResult.errors.length === 0 &&
    parseResult.txs.length > 0 &&
    optionsValid &&
    !jobId;

  return (
    <Modal
      open={isOpen}
      onClose={closeUploadModal}
      title="Upload transactions"
      closable={!jobId}
    >
      {/* Content */}
      <div className="flex-1 overflow-y-auto px-5 py-4">
        {jobId && jobStatus?.status === JobStatus.FAILED ? (
          <div className="space-y-3">
            <p className="font-medium text-accent-dark">Upload failed</p>
            {jobStatus.validationErrors.length > 0 && (
              <div>
                <p className="text-sm font-medium text-text-primary">Validation errors</p>
                <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                  {jobStatus.validationErrors.map((e, i) => (
                    <li key={i}>
                      Row {e.rowIndex + 1}: {e.field} &ndash; {e.message}
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
                      Row {e.rowIndex + 1}: {e.instrumentDescription} &ndash; {e.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <button
              type="button"
              onClick={closeUploadModal}
              className="rounded-md border border-border px-4 py-2 text-sm font-medium transition-colors hover:bg-primary-light/15"
            >
              Close
            </button>
          </div>
        ) : jobId ? (
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
                ? `Processed ${jobStatus.processedCount} of ${jobStatus.totalCount} transactions\u2026`
                : "Processing\u2026"}
            </p>
          </div>
        ) : step === 1 ? (
          <div className="space-y-4">
            {/* Step indicator */}
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className="text-primary-dark">1. Broker</span>
              <span className="h-px w-4 bg-border" />
              <span className="text-text-muted">2. File</span>
            </div>
            <p className="text-sm text-text-muted">Select the broker for this transaction file.</p>
            <div className="space-y-2">
              <label htmlFor="upload-broker" className="block text-sm font-medium text-text-primary">
                Broker
              </label>
              <select
                id="upload-broker"
                value={broker}
                onChange={(e) => {
                  setBroker(Number(e.target.value) as Broker);
                  setFormatId("standard");
                  setConverterOptions({});
                }}
                className="block w-full rounded-md border border-border bg-surface px-3 py-2 text-text-primary focus:border-primary focus:outline-none"
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
              className="rounded-md bg-primary px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-primary-dark"
            >
              Next
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            {/* Step indicator */}
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className="text-text-muted">1. Broker</span>
              <span className="h-px w-4 bg-border" />
              <span className="text-primary-dark">2. File</span>
            </div>
            <p className="text-sm text-text-muted">Choose format and select your CSV file.</p>
            <div className="space-y-2">
              <label htmlFor="upload-format" className="block text-sm font-medium text-text-primary">
                Format
              </label>
              <select
                id="upload-format"
                value={formatId}
                onChange={(e) => setFormatId(e.target.value)}
                className="block w-full rounded-md border border-border bg-surface px-3 py-2 text-text-primary focus:border-primary focus:outline-none"
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
              <label htmlFor="upload-file" className="block text-sm font-medium text-text-primary">
                CSV file
              </label>
              <input
                ref={fileInputRef}
                id="upload-file"
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
            {parseResult && (
              <div className="rounded-md border border-border bg-background p-4">
                {parseResult.errors.length > 0 ? (
                  <>
                    <p className="font-medium text-accent-dark">Parse errors</p>
                    <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                      {parseResult.errors.map((e, i) => (
                        <li key={i}>
                          Row {e.rowIndex}: {e.field} &ndash; {e.message}
                        </li>
                      ))}
                    </ul>
                  </>
                ) : (
                  <>
                    <p className="text-sm text-text-primary">
                      {parseResult.txs.length} transaction(s), from{" "}
                      {parseResult.periodFrom.toLocaleDateString()} to{" "}
                      {parseResult.periodTo.toLocaleDateString()}.
                    </p>
                    <button
                      type="button"
                      onClick={handleUpload}
                      disabled={!canUpload}
                      className="mt-3 rounded-md bg-accent px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-accent-dark disabled:opacity-50"
                    >
                      Upload
                    </button>
                  </>
                )}
              </div>
            )}
            {submitError && (
              <ErrorAlert>{submitError}</ErrorAlert>
            )}
            <button
              type="button"
              onClick={() => setStep(1)}
              className="text-sm text-text-muted underline hover:text-primary"
            >
              Back
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
}
