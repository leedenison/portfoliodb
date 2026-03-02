"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { SignInButton } from "@/app/components/sign-in";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { getHoldings } from "@/lib/portfolio-api";
import { Broker } from "@/gen/api/v1/api_pb";

function brokerLabel(broker: Broker): string {
  switch (broker) {
    case Broker.IBKR:
      return "IBKR";
    case Broker.SCHB:
      return "SCHB";
    default:
      return "";
  }
}

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
          <p className="text-slate-500">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center">
            <h1 className="text-4xl font-bold tracking-tight text-slate-800">
              Holdings
            </h1>
            <p className="mt-3 text-slate-600">Sign in to view holdings.</p>
            <p className="mt-6">
              <SignInButton />
            </p>
            {authError && (
              <p className="mt-4 rounded bg-red-50 px-4 py-2 text-sm text-red-700">
                {authError}
              </p>
            )}
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-2xl space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-xl font-semibold text-slate-800">
                Holdings
              </h2>
              <Link
                href="/upload"
                className="rounded bg-slate-800 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-700"
              >
                Upload transactions
              </Link>
            </div>
            {loading && (
              <p className="text-slate-500">Loading holdings…</p>
            )}
            {!loading && error && (
              <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">
                {error}
              </p>
            )}
            {!loading && !error && holdings && (
              <>
                {holdings.asOf && (
                  <p className="text-xs text-slate-500">
                    As of {holdings.asOf.toLocaleString()}
                  </p>
                )}
                <div className="overflow-x-auto rounded border border-slate-200 bg-white">
                  <table className="w-full min-w-[320px] border-collapse text-sm">
                    <thead>
                      <tr className="border-b border-slate-200 bg-slate-50">
                        <th className="px-4 py-2 text-left font-medium text-slate-700">
                          Instrument
                        </th>
                        <th className="px-4 py-2 text-right font-medium text-slate-700">
                          Quantity
                        </th>
                        <th className="px-4 py-2 text-left font-medium text-slate-700">
                          Account
                        </th>
                        <th className="px-4 py-2 text-left font-medium text-slate-700">
                          Broker
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {holdings.holdings.length === 0 ? (
                        <tr>
                          <td
                            colSpan={4}
                            className="px-4 py-6 text-center text-slate-500"
                          >
                            No holdings. Upload transactions to get started.
                          </td>
                        </tr>
                      ) : (
                        holdings.holdings.map((h, i) => (
                          <tr
                            key={i}
                            className="border-b border-slate-100 last:border-0"
                          >
                            <td className="px-4 py-2 text-slate-800">
                              {h.instrumentDescription || "—"}
                            </td>
                            <td className="px-4 py-2 text-right tabular-nums text-slate-800">
                              {h.quantity}
                            </td>
                            <td className="px-4 py-2 text-slate-600">
                              {h.account || "—"}
                            </td>
                            <td className="px-4 py-2 text-slate-600">
                              {brokerLabel(h.broker) || "—"}
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
