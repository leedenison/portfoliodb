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
            <h1 className="text-4xl font-bold tracking-tight text-text-primary">
              Holdings
            </h1>
            <p className="mt-3 text-text-muted">Sign in to view holdings.</p>
            {authError && (
              <p className="mt-4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-2xl space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-xl font-semibold text-text-primary">
                Holdings
              </h2>
              <Link
                href="/upload"
                className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-accent-dark"
              >
                Upload transactions
              </Link>
            </div>
            {loading && (
              <p className="text-text-muted">Loading holdings…</p>
            )}
            {!loading && error && (
              <p className="rounded-lg bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
                {error}
              </p>
            )}
            {!loading && !error && holdings && (
              <>
                {holdings.asOf && (
                  <p className="text-xs text-text-muted">
                    As of {holdings.asOf.toLocaleString()}
                  </p>
                )}
                <div className="overflow-x-auto rounded-lg border border-border bg-surface shadow-sm">
                  <table className="w-full min-w-[320px] border-collapse text-sm">
                    <thead>
                      <tr className="border-b border-border bg-primary-light/20">
                        <th className="px-4 py-2.5 text-left font-medium text-text-primary">
                          Instrument
                        </th>
                        <th className="px-4 py-2.5 text-right font-medium text-text-primary">
                          Quantity
                        </th>
                        <th className="px-4 py-2.5 text-left font-medium text-text-primary">
                          Account
                        </th>
                        <th className="px-4 py-2.5 text-left font-medium text-text-primary">
                          Broker
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {holdings.holdings.length === 0 ? (
                        <tr>
                          <td
                            colSpan={4}
                            className="px-4 py-6 text-center text-text-muted"
                          >
                            No holdings. Upload transactions to get started.
                          </td>
                        </tr>
                      ) : (
                        holdings.holdings.map((h, i) => (
                          <tr
                            key={i}
                            className="border-b border-border/50 last:border-0"
                          >
                            <td className="px-4 py-2.5 text-text-primary">
                              {h.instrumentDescription || "—"}
                            </td>
                            <td className="px-4 py-2.5 text-right tabular-nums text-text-primary">
                              {h.quantity}
                            </td>
                            <td className="px-4 py-2.5 text-text-muted">
                              {h.account || "—"}
                            </td>
                            <td className="px-4 py-2.5 text-text-muted">
                              {getBrokerLabel(h.broker)}
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
