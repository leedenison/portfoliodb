"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { getHoldings } from "@/lib/portfolio-api";
import { getBrokerLabel } from "@/lib/csv/converters";

export default function UserHoldingsPage() {
  const { state, authError } = useAuth();
  const [holdings, setHoldings] = useState<Awaited<ReturnType<typeof getHoldings>> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const h = await getHoldings({});
      setHoldings(h);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setHoldings(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (state.status !== "authenticated") return;
    fetchData();
  }, [state.status, fetchData]);

  return (
    <AppShell>
      <div className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Holdings
            </h1>
            <p className="mt-3 text-text-muted">Sign in to view holdings.</p>
            {authError && (
              <p className="mt-4 rounded-md bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-4xl animate-fade-in space-y-5">
            <div className="flex flex-wrap items-baseline gap-3">
              <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                Holdings
              </h2>
              {holdings?.asOf && (
                <span className="font-mono text-xs text-text-muted">
                  as of {holdings.asOf.toLocaleString()}
                </span>
              )}
              <Link
                href="/upload"
                className="ml-auto rounded-md bg-accent px-3.5 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
              >
                Upload transactions
              </Link>
            </div>
            {loading && (
              <p className="text-text-muted">Loading holdings…</p>
            )}
            {!loading && error && (
              <p className="rounded-md bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
                {error}
              </p>
            )}
            {!loading && !error && holdings && (
              <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
                <table className="w-full min-w-[320px] border-collapse text-sm">
                  <thead>
                    <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Instrument
                      </th>
                      <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Quantity
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Account
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Broker
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {holdings.holdings.length === 0 ? (
                      <tr>
                        <td
                          colSpan={4}
                          className="px-4 py-8 text-center text-text-muted"
                        >
                          No holdings. Upload transactions to get started.
                        </td>
                      </tr>
                    ) : (
                      holdings.holdings.map((h, i) => (
                        <tr
                          key={i}
                          className="border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
                        >
                          <td className="px-4 py-3 font-medium text-text-primary">
                            {h.instrumentDescription || "—"}
                          </td>
                          <td className="px-4 py-3 text-right font-mono tabular-nums text-text-primary">
                            {h.quantity}
                          </td>
                          <td className="px-4 py-3 text-text-muted">
                            {h.account || "—"}
                          </td>
                          <td className="px-4 py-3 text-text-muted">
                            {getBrokerLabel(h.broker)}
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
