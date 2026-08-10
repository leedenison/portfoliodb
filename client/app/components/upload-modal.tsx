"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { Modal } from "@/app/components/modal";
import { useUploadModal } from "@/contexts/upload-modal-context";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { getJob } from "@/lib/portfolio-api";
import { upsertTxs } from "@/lib/ingestion-api";
import { parseStandardCSV } from "@/lib/csv/standard";
import { JobStatus } from "@/gen/api/v1/api_pb";
import { Broker } from "@/gen/type/v1/type_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { lastCoveredDay } from "@/lib/dates";
import {
  getBrokerOptionsForUpload,
  getFormatsForBroker,
  getSourcePrefix,
} from "@/lib/csv/converters";

const BROKER_OPTIONS = getBrokerOptionsForUpload();
const DEFAULT_BROKER = BROKER_OPTIONS[0]?.value ?? Broker.FIDELITY;

/**
 * Shell. The body is mounted only while the modal is open, so every field it
 * owns starts fresh on each open -- which is what the reset-on-open effect used
 * to do. jobId stays here because the shell needs it to decide whether the
 * modal can be dismissed.
 */
export function UploadModal() {
  const { isOpen, closeUploadModal } = useUploadModal();
  const [jobId, setJobId] = useState<string | null>(null);

  return (
    <Modal
      open={isOpen}
      onClose={closeUploadModal}
      title="Upload transactions"
      closable={!jobId}
      data-testid="upload-modal"
    >
      {isOpen && <UploadModalBody jobId={jobId} onJobStarted={setJobId} />}
    </Modal>
  );
}

function UploadModalBody({
  jobId,
  onJobStarted,
}: {
  jobId: string | null;
  onJobStarted: (id: string | null) => void;
}) {
  const { closeUploadModal, onComplete } = useUploadModal();
  const [step, setStep] = useState<1 | 2>(1);
  const [broker, setBroker] = useState<Broker>(DEFAULT_BROKER);
  const [formatId, setFormatId] = useState<string>("standard");
  const [converterOptions, setConverterOptions] = useState<Record<string, unknown>>({});
  const [file, setFile] = useState<File | null>(null);
  const [fileText, setFileText] = useState<string | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [fileInputActive, setFileInputActive] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const formats = useMemo(() => getFormatsForBroker(broker), [broker]);
  const selectedFormat = useMemo(() => formats.find((f) => f.id === formatId), [formats, formatId]);
  const optionsValid =
    selectedFormat?.OptionsComponent == null ||
    (converterOptions?.currency != null && converterOptions?.currency !== "");

  // Reading the file is the event; parsing it is derivation. Holding the text
  // rather than the parsed rows means a format or option change re-parses
  // without re-reading, and without an effect to clear the stale result.
  const parseResult = useMemo(() => {
    if (fileText == null || !optionsValid) return null;
    return selectedFormat?.convert
      ? selectedFormat.convert(fileText, converterOptions)
      : parseStandardCSV(fileText);
  }, [fileText, selectedFormat, converterOptions, optionsValid]);

  const handleFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    setFile(f ?? null);
    setFileText(null);
    setSubmitError(null);
    if (!f) return;
    const reader = new FileReader();
    reader.onload = () => {
      setFileText(typeof reader.result === "string" ? reader.result : "");
    };
    reader.readAsText(f);
  }, []);

  const handleUpload = useCallback(async () => {
    if (!parseResult || parseResult.errors.length > 0 || parseResult.postings.length === 0) return;
    setSubmitError(null);
    try {
      const sourcePrefix = getSourcePrefix(broker);
      const source = `${sourcePrefix}:web:${formatId}`;
      const res = await upsertTxs({
        window: {
          broker,
          source,
          periodFrom: timestampFromDate(parseResult.periodFrom),
          periodBefore: timestampFromDate(parseResult.periodBefore),
          postings: parseResult.postings,
        },
        filename: file?.name,
      });
      onJobStarted(res.jobId);
    } catch (e) {
      setSubmitError(errorMessage(e));
    }
  }, [broker, formatId, parseResult, file, onJobStarted]);

  // Poll until the job reaches a terminal status, then stop.
  const { data: jobStatus } = useAuthedQuery<Awaited<ReturnType<typeof getJob>>>({
    queryKey: qk.job(jobId ?? ""),
    queryFn: () => getJob(jobId!),
    enabled: !!jobId,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === JobStatus.SUCCESS || status === JobStatus.FAILED ? false : 2000;
    },
  });

  // Closing on success is a genuine side effect -- telling the caller to refresh
  // and dismissing the modal -- so it belongs in an effect, not in render.
  const succeeded = jobStatus?.status === JobStatus.SUCCESS;
  useEffect(() => {
    if (!succeeded) return;
    onComplete?.();
    closeUploadModal();
  }, [succeeded, onComplete, closeUploadModal]);

  const canUpload =
    parseResult &&
    parseResult.errors.length === 0 &&
    parseResult.postings.length > 0 &&
    optionsValid &&
    !jobId;

  return (
    <>
      {/* Content */}
      <div className="flex-1 overflow-y-auto px-5 py-4">
        {jobId && jobStatus?.status === JobStatus.FAILED ? (
          <div className="space-y-3">
            <p className="font-medium text-accent-dark">Upload failed</p>
            {jobStatus.validationErrors.length > 0 && (
              <div data-testid="upload-validation-errors">
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
              <div data-testid="upload-identification-errors">
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
          <div data-testid="job-status-badge" className="flex flex-col items-center gap-3 py-6">
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
                data-testid="select-broker"
                value={broker}
                onChange={(e) => {
                  setBroker(Number(e.target.value) as Broker);
                  setFormatId("standard");
                  setConverterOptions({});
                }}
                className="block w-full rounded-md border border-border bg-surface px-3 py-2 text-text-primary focus:border-primary focus:outline-hidden"
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
            <p className="text-sm text-text-muted">Choose format and select your transaction file.</p>
            <div className="space-y-2">
              <label htmlFor="upload-format" className="block text-sm font-medium text-text-primary">
                Format
              </label>
              <select
                id="upload-format"
                value={formatId}
                onChange={(e) => setFormatId(e.target.value)}
                className="block w-full rounded-md border border-border bg-surface px-3 py-2 text-text-primary focus:border-primary focus:outline-hidden"
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
                Transaction file
              </label>
              <input
                ref={fileInputRef}
                id="upload-file"
                type="file"
                accept={selectedFormat?.accept ?? ".csv"}
                onChange={handleFileChange}
                className="sr-only"
                aria-label="Choose transaction file"
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
                  <div data-testid="upload-parse-errors">
                    <p className="font-medium text-accent-dark">Parse errors</p>
                    <ul className="mt-1 list-inside list-disc text-sm text-text-muted">
                      {parseResult.errors.map((e, i) => (
                        <li key={i}>
                          Row {e.rowIndex}: {e.field} &ndash; {e.message}
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : (
                  <>
                    <div data-testid="upload-parse-preview" className="text-sm text-text-primary">
                      {parseResult.postings.length} posting(s), from{" "}
                      {parseResult.periodFrom.toLocaleDateString()} to{" "}
                      {lastCoveredDay(parseResult.periodBefore).toLocaleDateString()}.
                    </div>
                    <button
                      type="button"
                      data-testid="btn-upload-submit"
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
    </>
  );
}
