"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Modal } from "@/app/components/modal";
import { ErrorAlert } from "@/app/components/error-alert";
import { Broker, JobStatus } from "@/gen/api/v1/api_pb";
import {
  getBrokerOptionsForSplitExtraction,
  getSplitExtractorsForBroker,
  type ExtractedSplit,
  type SplitParseResult,
} from "@/lib/csv/converters";
import {
  getHoldings,
  getJob,
  importCorporateEventSplits,
  type GetJobResult,
} from "@/lib/portfolio-api";
import { prefillFromPosition, validateRatio } from "@/lib/splits";
import type { Holding } from "@/gen/api/v1/api_pb";

type Phase = "idle" | "preview" | "processing" | "result";

interface PreviewRow extends ExtractedSplit {
  /** Editable user inputs (defaults from extractor or prefill). */
  splitFromInput: string;
  splitToInput: string;
  /** Set when prefill chose values for the user. */
  prefilled: boolean;
}

const BROKER_OPTIONS = getBrokerOptionsForSplitExtraction();
const DEFAULT_BROKER = BROKER_OPTIONS[0]?.value;

export function ImportCorporateEventsModal({
  open,
  onClose,
  onComplete,
}: {
  open: boolean;
  onClose: () => void;
  onComplete?: () => void;
}) {
  const [phase, setPhase] = useState<Phase>("idle");
  const [broker, setBroker] = useState<Broker | undefined>(DEFAULT_BROKER);
  const [extractorId, setExtractorId] = useState<string>("");
  const [parseResult, setParseResult] = useState<SplitParseResult | null>(null);
  const [rows, setRows] = useState<PreviewRow[]>([]);
  const [prefilling, setPrefilling] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [fileInputActive, setFileInputActive] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [jobStatus, setJobStatus] = useState<GetJobResult | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const extractors = useMemo(
    () => (broker != null ? getSplitExtractorsForBroker(broker) : []),
    [broker],
  );
  const selectedExtractor = useMemo(
    () => extractors.find((e) => e.id === extractorId) ?? extractors[0],
    [extractors, extractorId],
  );

  const reset = useCallback(() => {
    setPhase("idle");
    setBroker(DEFAULT_BROKER);
    setExtractorId("");
    setParseResult(null);
    setRows([]);
    setPrefilling(false);
    setFile(null);
    setFileInputActive(false);
    setSubmitError(null);
    setJobId(null);
    setJobStatus(null);
    if (fileRef.current) fileRef.current.value = "";
  }, []);

  // Reset on close.
  useEffect(() => {
    if (!open) reset();
  }, [open, reset]);

  // Auto-prefill ratios from admin holdings after file parse.
  const autoPrefill = useCallback(async (parsed: PreviewRow[], selectedBroker: Broker | undefined) => {
    const needsPrefill = parsed.some((r) => !r.splitFromInput || !r.splitToInput);
    if (!needsPrefill) return parsed;

    setPrefilling(true);
    try {
      const cache = new Map<string, Holding[]>();
      const next: PreviewRow[] = [];
      for (const row of parsed) {
        if (row.splitFromInput && row.splitToInput) {
          next.push(row);
          continue;
        }
        let holdings = cache.get(row.exDate);
        if (!holdings) {
          const res = await getHoldings({ asOf: dayBefore(row.exDate) });
          holdings = res.holdings;
          cache.set(row.exDate, holdings);
        }
        const pre = sumMatchingHoldingQty(holdings, row, selectedBroker);
        const delta = parseFloat(row.deltaShares ?? "");
        if (!Number.isFinite(pre) || !Number.isFinite(delta)) {
          next.push(row);
          continue;
        }
        const guess = prefillFromPosition(pre, delta);
        if (guess.splitFrom == null || guess.splitTo == null) {
          next.push(row);
          continue;
        }
        next.push({
          ...row,
          splitFromInput: guess.splitFrom,
          splitToInput: guess.splitTo,
          prefilled: true,
        });
      }
      return next;
    } catch {
      // Non-fatal: prefill just doesn't happen.
      return parsed;
    } finally {
      setPrefilling(false);
    }
  }, []);

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f || !selectedExtractor) return;
    setFile(f);
    const file = f;
    const reader = new FileReader();
    reader.onload = async (ev) => {
      const text = (ev.target?.result as string) ?? "";
      const result = selectedExtractor.extract(text);
      setParseResult(result);
      const preview = result.splits.map((s) => ({
        ...s,
        splitFromInput: s.splitFrom ?? "",
        splitToInput: s.splitTo ?? "",
        prefilled: false,
      }));
      setRows(preview);
      setPhase("preview");
      // Auto-prefill rows missing ratios.
      const prefilled = await autoPrefill(preview, broker);
      setRows(prefilled);
    };
    reader.readAsText(file);
  }

  function updateRow(index: number, patch: Partial<PreviewRow>) {
    setRows((prev) => prev.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  }

  // Validate every row before allowing submit.
  const rowValidation = useMemo(
    () => rows.map((r) => validateRatio(r.splitFromInput, r.splitToInput)),
    [rows],
  );
  const canSubmit =
    rows.length > 0 &&
    rowValidation.every((v) => v.ok) &&
    phase === "preview" &&
    !jobId &&
    !prefilling;

  async function handleSubmit() {
    if (!canSubmit) return;
    setSubmitError(null);
    setPhase("processing");
    try {
      const id = await importCorporateEventSplits(
        rows.map((r) => ({
          identifierType: r.identifier.type,
          identifierValue: r.identifier.value,
          identifierDomain: r.identifier.domain,
          exDate: r.exDate,
          splitFrom: r.splitFromInput,
          splitTo: r.splitToInput,
        })),
      );
      setJobId(id);
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : String(e));
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

  function handleClose() {
    if (phase === "processing") return;
    reset();
    onClose();
  }

  const accept = selectedExtractor?.accept ?? ".csv,.ofx,.qfx";

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Import corporate events"
      closable={phase !== "processing"}
    >
      <div className="flex max-h-[80vh] flex-col gap-4 overflow-y-auto p-5">
        {phase === "idle" && (
          <div className="space-y-4">
            <div className="space-y-2">
              <label htmlFor="ce-broker" className="block text-sm font-medium text-text-primary">
                Broker
              </label>
              <select
                id="ce-broker"
                value={broker ?? ""}
                onChange={(e) => {
                  setBroker(Number(e.target.value) as Broker);
                  setExtractorId("");
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
            {extractors.length > 1 && (
              <div className="space-y-2">
                <label htmlFor="ce-format" className="block text-sm font-medium text-text-primary">
                  Format
                </label>
                <select
                  id="ce-format"
                  value={selectedExtractor?.id ?? ""}
                  onChange={(e) => setExtractorId(e.target.value)}
                  className="block w-full rounded-md border border-border bg-surface px-3 py-2 text-text-primary focus:border-primary focus:outline-none"
                >
                  {extractors.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.label}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <div className="space-y-2">
              <label className="block text-sm font-medium text-text-primary">
                Transaction file
              </label>
              <input
                ref={fileRef}
                type="file"
                accept={accept}
                onChange={handleFileChange}
                className="sr-only"
                aria-label="Choose transaction file"
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
                    : "border-border bg-primary-light/20 text-text-primary hover:bg-primary-light/40"
                }`}
              >
                {fileInputActive ? "Opening\u2026" : "Choose file"}
              </button>
              {file && (
                <p className="text-sm text-text-muted">Selected: {file.name}</p>
              )}
            </div>
          </div>
        )}

        {phase === "preview" && parseResult && (
          <div className="space-y-4">
            {parseResult.errors.length > 0 && (
              <div className="space-y-2">
                <p className="text-sm font-medium text-accent-dark">
                  Parse errors ({parseResult.errors.length})
                </p>
                <ul className="max-h-32 overflow-y-auto rounded-md border border-border bg-surface text-xs">
                  {parseResult.errors.map((e, i) => (
                    <li key={i} className="border-b border-border/40 px-3 py-1.5 last:border-0">
                      Row {e.rowIndex}: {e.field} &ndash; {e.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {rows.length === 0 ? (
              <p className="text-sm text-text-muted">
                No SPLIT events found in this file.
              </p>
            ) : (
              <>
                {prefilling && (
                  <p className="text-sm text-text-muted">Prefilling ratios from your holdings...</p>
                )}

                <div className="overflow-x-auto rounded-md border border-border">
                  <table className="w-full min-w-[640px] border-collapse text-xs">
                    <thead>
                      <tr className="border-b border-border bg-primary-dark/[0.03] text-left">
                        <th className="px-3 py-2 font-semibold uppercase tracking-wider text-text-muted">Ex date</th>
                        <th className="px-3 py-2 font-semibold uppercase tracking-wider text-text-muted">Instrument</th>
                        <th className="px-3 py-2 font-semibold uppercase tracking-wider text-text-muted">From</th>
                        <th className="px-3 py-2 font-semibold uppercase tracking-wider text-text-muted">To</th>
                        <th className="px-3 py-2 font-semibold uppercase tracking-wider text-text-muted">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((row, i) => {
                        const v = rowValidation[i];
                        return (
                          <tr key={`${row.identifier.value}-${row.exDate}-${i}`} className="border-b border-border/40 last:border-0">
                            <td className="px-3 py-1.5 font-mono text-text-muted">{row.exDate}</td>
                            <td className="px-3 py-1.5 text-text-primary">
                              <div>{row.instrumentDescription}</div>
                              <div className="font-mono text-[10px] text-text-muted">
                                {row.identifier.type}:{row.identifier.value}
                              </div>
                            </td>
                            <td className="px-3 py-1.5">
                              <input
                                type="text"
                                inputMode="decimal"
                                value={row.splitFromInput}
                                onChange={(e) => updateRow(i, { splitFromInput: e.target.value, prefilled: false })}
                                className="w-20 rounded border border-border bg-surface px-2 py-1 font-mono text-xs text-text-primary focus:border-primary focus:outline-none"
                              />
                            </td>
                            <td className="px-3 py-1.5">
                              <input
                                type="text"
                                inputMode="decimal"
                                value={row.splitToInput}
                                onChange={(e) => updateRow(i, { splitToInput: e.target.value, prefilled: false })}
                                className="w-20 rounded border border-border bg-surface px-2 py-1 font-mono text-xs text-text-primary focus:border-primary focus:outline-none"
                              />
                            </td>
                            <td className="px-3 py-1.5">
                              {v.error ? (
                                <span className="text-accent-dark">{v.error}</span>
                              ) : v.warning ? (
                                <span className="text-amber-600">{v.warning}</span>
                              ) : row.prefilled ? (
                                <span className="text-text-muted">prefilled</span>
                              ) : (
                                <span className="text-text-muted">ok</span>
                              )}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>

                {submitError && <ErrorAlert>{submitError}</ErrorAlert>}

                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={handleSubmit}
                    disabled={!canSubmit}
                    className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-dark disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    Import {rows.length} split{rows.length !== 1 ? "s" : ""}
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
          </div>
        )}

        {phase === "processing" && (
          <div className="flex flex-col items-center gap-3 py-6">
            <svg className="h-8 w-8 animate-spin text-primary" viewBox="0 0 24 24" fill="none">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v3a5 5 0 00-5 5H4z" />
            </svg>
            <p className="text-sm text-text-muted">
              {jobStatus && jobStatus.totalCount > 0
                ? `Processed ${jobStatus.processedCount} of ${jobStatus.totalCount}\u2026`
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
                event{jobStatus.processedCount !== 1 ? "s" : ""} processed.
              </p>
            ) : (
              <p className="text-sm font-medium text-accent-dark">Import failed.</p>
            )}
            {jobStatus.validationErrors.length > 0 && (
              <ul className="max-h-32 overflow-y-auto rounded-md border border-border bg-surface text-xs">
                {jobStatus.validationErrors.map((e, i) => (
                  <li key={i} className="border-b border-border/40 px-3 py-1.5 last:border-0">
                    Row {e.rowIndex}: {e.field} &ndash; {e.message}
                  </li>
                ))}
              </ul>
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

// ── Helpers ─────────────────────────────────────────────────────────

function dayBefore(ymd: string): Date {
  const [y, m, d] = ymd.split("-").map(Number);
  // Use UTC midnight on the day before ex date.
  return new Date(Date.UTC(y, m - 1, d - 1));
}

/**
 * Sum signed quantities of holdings that match the SPLIT row's
 * instrument. We match on identifier value (case-insensitive), falling
 * back to instrument description equality. When the row has an account,
 * holdings outside that account are ignored; otherwise all matching
 * holdings are summed. Holdings are also filtered by broker when provided.
 */
function sumMatchingHoldingQty(
  holdings: Holding[],
  row: ExtractedSplit,
  selectedBroker?: Broker,
): number {
  const idValue = row.identifier.value.toUpperCase();
  const desc = row.instrumentDescription.toUpperCase();
  let total = 0;
  let any = false;
  for (const h of holdings) {
    if (selectedBroker != null && h.broker !== selectedBroker) continue;
    if (row.account && h.account !== row.account) continue;
    const matchesId = h.instrument?.identifiers.some(
      (id) => id.value.toUpperCase() === idValue,
    );
    const matchesDesc = h.instrumentDescription.toUpperCase() === desc;
    if (matchesId || matchesDesc) {
      total += h.quantity;
      any = true;
    }
  }
  return any ? total : NaN;
}
