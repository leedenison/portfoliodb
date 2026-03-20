"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { PerformanceChart } from "@/app/components/performance-chart";
import { ErrorAlert } from "@/app/components/error-alert";
import { useAuth } from "@/contexts/auth-context";
import { usePortfolio } from "@/contexts/portfolio-context";
import {
  getPortfolioValuation,
  type ValuationPointUI,
} from "@/lib/portfolio-api";

type Period = "3m" | "6m" | "1y" | "2y" | "5y";

const periods: { key: Period; label: string }[] = [
  { key: "3m", label: "3M" },
  { key: "6m", label: "6M" },
  { key: "1y", label: "1Y" },
  { key: "2y", label: "2Y" },
  { key: "5y", label: "5Y" },
];

function dateFromPeriod(period: Period): string {
  const d = new Date();
  switch (period) {
    case "3m":
      d.setMonth(d.getMonth() - 3);
      break;
    case "6m":
      d.setMonth(d.getMonth() - 6);
      break;
    case "1y":
      d.setFullYear(d.getFullYear() - 1);
      break;
    case "2y":
      d.setFullYear(d.getFullYear() - 2);
      break;
    case "5y":
      d.setFullYear(d.getFullYear() - 5);
      break;
  }
  return d.toISOString().slice(0, 10);
}

function todayStr(): string {
  return new Date().toISOString().slice(0, 10);
}

export default function PerformancePage() {
  const { state, authError } = useAuth();
  const { selected: selectedPortfolio } = usePortfolio();
  const [period, setPeriod] = useState<Period>("1y");
  const [points, setPoints] = useState<ValuationPointUI[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const dateRange = useMemo(
    () => ({ dateFrom: dateFromPeriod(period), dateTo: todayStr() }),
    [period]
  );

  const fetchData = useCallback(
    async (portfolioId: string, from: string, to: string) => {
      setLoading(true);
      setError(null);
      try {
        const res = await getPortfolioValuation({
          portfolioId,
          dateFrom: from,
          dateTo: to,
        });
        setPoints(res.points);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        setPoints([]);
      } finally {
        setLoading(false);
      }
    },
    []
  );

  useEffect(() => {
    if (state.status !== "authenticated" || !selectedPortfolio) return;
    fetchData(selectedPortfolio.id, dateRange.dateFrom, dateRange.dateTo);
  }, [state.status, selectedPortfolio, dateRange, fetchData]);

  // Compute percentage change.
  const pctChange = useMemo(() => {
    if (points.length < 2) return null;
    const first = points[0].totalValue;
    const last = points[points.length - 1].totalValue;
    if (first === 0) return null;
    return ((last - first) / first) * 100;
  }, [points]);

  const periodLabel = periods.find((p) => p.key === period)?.label ?? "";

  return (
    <AppShell>
      <div className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading...</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Performance
            </h1>
            <p className="mt-3 text-text-muted">
              Sign in to view performance.
            </p>
            {authError && (
              <p className="mt-4 rounded-md bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-4xl animate-fade-in space-y-5">
            {!selectedPortfolio ? (
              <div className="flex flex-1 flex-col items-center justify-center py-20 text-center">
                <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                  Performance
                </h2>
                <p className="mt-3 text-text-muted">
                  Select a portfolio to view performance.
                </p>
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-baseline gap-3">
                  <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                    {selectedPortfolio.name}
                  </h2>
                  {pctChange !== null && (
                    <span
                      className={`font-mono text-sm font-semibold ${
                        pctChange >= 0 ? "text-green-600 dark:text-green-400" : "text-red-600 dark:text-red-400"
                      }`}
                    >
                      {pctChange >= 0 ? "+" : ""}
                      {pctChange.toFixed(1)}% over {periodLabel.toLowerCase()}
                    </span>
                  )}
                  <div className="ml-auto flex gap-1">
                    {periods.map((p) => (
                      <button
                        key={p.key}
                        type="button"
                        onClick={() => setPeriod(p.key)}
                        className={`rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${
                          period === p.key
                            ? "bg-primary-dark/10 text-primary-dark dark:bg-primary-light/20 dark:text-primary-light"
                            : "text-text-muted hover:bg-primary-light/15 hover:text-text-primary"
                        }`}
                      >
                        {p.label}
                      </button>
                    ))}
                  </div>
                </div>

                {loading && (
                  <p className="text-text-muted">Loading valuation data...</p>
                )}
                {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
                {!loading && !error && <PerformanceChart points={points} />}
              </>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
