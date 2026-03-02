"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { SignInButton } from "@/app/components/sign-in";
import { AppHeader } from "@/app/components/app-header";
import { useAuth } from "@/contexts/auth-context";
import { getJob, getPortfolio } from "@/lib/portfolio-api";
import type { Portfolio } from "@/lib/portfolio-api";
import { upsertTxs } from "@/lib/ingestion-api";
import { parseStandardCSV } from "@/lib/csv/standard";
import { Broker } from "@/gen/api/v1/api_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { JobStatus } from "@/gen/api/v1/api_pb";

const BROKER_OPTIONS: { value: Broker; label: string }[] = [
  { value: Broker.IBKR, label: "IBKR" },
  { value: Broker.SCHB, label: "Charles Schwab" },
];

function brokerToSourcePrefix(broker: Broker): string {
  switch (broker) {
    case Broker.IBKR:
      return "IBKR";
    case Broker.SCHB:
      return "SCHB";
    default:
      return "unknown";
  }
}

export default function UploadTransactionsPage() {
  const params = useParams();
  const portfolioId = typeof params?.id === "string" ? params.id : "";
  const { state, authError } = useAuth();
  const [portfolio, setPortfolio] = useState<Portfolio | null>(null);
  const [step, setStep] = useState<1 | 2>(1);
  const [broker, setBroker] = useState<Broker>(Broker.IBKR);
  const [file, setFile] = useState<File | null>(null);
  const [parseResult, setParseResult] = useState<ReturnType<typeof parseStandardCSV> | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [jobStatus, setJobStatus] = useState<Awaited<ReturnType<typeof getJob>> | null>(null);
  const [loading, setLoading] = useState(true);
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!portfolioId || state.status !== "authenticated") return;
    let cancelled = false;
    getPortfolio(portfolioId)
      .then((p) => {
        if (!cancelled) setPortfolio(p);
      })
      .catch(() => {
        if (!cancelled) setPortfolio(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [portfolioId, state.status]);

  const handleFileChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    setFile(f ?? null);
    setParseResult(null);
    setSubmitError(null);
    if (!f) return;
    const reader = new FileReader();
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      const result = parseStandardCSV(text);
      setParseResult(result);
    };
    reader.readAsText(f);
  }, []);

  const handleUpload = useCallback(async () => {
    if (!portfolioId || !parseResult || parseResult.errors.length > 0 || parseResult.txs.length === 0) return;
    setSubmitError(null);
    try {
      const source = `${brokerToSourcePrefix(broker)}:web:standard`;
      const res = await upsertTxs({
        portfolioId,
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
  }, [portfolioId, broker, parseResult]);

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

  if (state.status === "loading" || (state.status === "authenticated" && loading && !portfolio)) {
    return (
      <main className="flex min-h-screen flex-col">
        <AppHeader />
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-slate-500">Loading…</p>
        </div>
      </main>
    );
  }

  if (state.status === "unauthenticated") {
    return (
      <main className="flex min-h-screen flex-col">
        <AppHeader />
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8">
          <h1 className="text-4xl font-bold tracking-tight text-slate-800">Upload transactions</h1>
          <p className="mt-3 text-slate-600">Sign in to upload.</p>
          <p className="mt-6">
            <SignInButton />
          </p>
          {authError && (
            <p className="mt-4 rounded bg-red-50 px-4 py-2 text-sm text-red-700">{authError}</p>
          )}
        </div>
      </main>
    );
  }

  if (!portfolioId || !portfolio) {
    return (
      <main className="flex min-h-screen flex-col">
        <AppHeader />
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-slate-600">Portfolio not found.</p>
          <Link href="/" className="ml-2 text-slate-600 underline hover:text-slate-800">
            Back to portfolios
          </Link>
        </div>
      </main>
    );
  }

  const canUpload =
    parseResult &&
    parseResult.errors.length === 0 &&
    parseResult.txs.length > 0 &&
    !jobId;

  return (
    <main className="flex min-h-screen flex-col">
      <AppHeader />
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        <div className="w-full max-w-2xl space-y-4">
          <Link
            href={`/portfolios/${portfolioId}`}
            className="text-sm text-slate-600 underline hover:text-slate-800"
          >
            Back to holdings
          </Link>
          <h2 className="text-xl font-semibold text-slate-800">
            Upload transactions – {portfolio.name}
          </h2>

          {jobId && jobStatus && (jobStatus.status === JobStatus.SUCCESS || jobStatus.status === JobStatus.FAILED) ? (
            <div className="space-y-3 rounded border border-slate-200 bg-white p-4">
              {jobStatus.status === JobStatus.SUCCESS ? (
                <>
                  <p className="text-slate-800">Upload completed successfully.</p>
                  <Link
                    href={`/portfolios/${portfolioId}`}
                    className="inline-block text-sm text-slate-600 underline hover:text-slate-800"
                  >
                    View holdings
                  </Link>
                </>
              ) : (
                <>
                  <p className="font-medium text-red-700">Upload failed</p>
                  {jobStatus.validationErrors.length > 0 && (
                    <div>
                      <p className="text-sm font-medium text-slate-700">Validation errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-slate-600">
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
                      <p className="text-sm font-medium text-slate-700">Identification errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-slate-600">
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
            <p className="text-slate-600">Processing…</p>
          ) : step === 1 ? (
            <>
              <p className="text-slate-600">Select the broker for this transaction file.</p>
              <div className="space-y-2">
                <label htmlFor="broker" className="block text-sm font-medium text-slate-700">
                  Broker
                </label>
                <select
                  id="broker"
                  value={broker}
                  onChange={(e) => setBroker(Number(e.target.value) as Broker)}
                  className="block w-full rounded border border-slate-300 bg-white px-3 py-2 text-slate-800"
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
                className="rounded bg-slate-800 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700"
              >
                Next
              </button>
            </>
          ) : (
            <>
              <p className="text-slate-600">
                Choose <strong>Standard</strong> format and select your CSV file.
              </p>
              <div className="space-y-2">
                <label className="block text-sm font-medium text-slate-700">Format</label>
                <p className="rounded border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-700">
                  Standard
                </p>
              </div>
              <div className="space-y-2">
                <label htmlFor="file" className="block text-sm font-medium text-slate-700">
                  CSV file
                </label>
                <input
                  ref={fileInputRef}
                  id="file"
                  type="file"
                  accept=".csv"
                  onChange={handleFileChange}
                  className="block w-full text-sm text-slate-600 file:mr-4 file:rounded file:border-0 file:bg-slate-100 file:px-4 file:py-2 file:text-slate-700"
                />
              </div>
              {parseResult && (
                <div className="rounded border border-slate-200 bg-white p-4">
                  {parseResult.errors.length > 0 ? (
                    <>
                      <p className="font-medium text-red-700">Parse errors</p>
                      <ul className="mt-1 list-inside list-disc text-sm text-slate-600">
                        {parseResult.errors.map((e, i) => (
                          <li key={i}>
                            Row {e.rowIndex}: {e.field} – {e.message}
                          </li>
                        ))}
                      </ul>
                    </>
                  ) : (
                    <>
                      <p className="text-slate-800">
                        {parseResult.txs.length} transaction(s), from{" "}
                        {parseResult.periodFrom.toLocaleDateString()} to{" "}
                        {parseResult.periodTo.toLocaleDateString()}.
                      </p>
                      <button
                        type="button"
                        onClick={handleUpload}
                        disabled={!canUpload}
                        className="mt-3 rounded bg-slate-800 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
                      >
                        Upload
                      </button>
                    </>
                  )}
                </div>
              )}
              {submitError && (
                <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">{submitError}</p>
              )}
              <button
                type="button"
                onClick={() => setStep(1)}
                className="text-sm text-slate-600 underline hover:text-slate-800"
              >
                Back
              </button>
            </>
          )}
        </div>
      </div>
    </main>
  );
}
