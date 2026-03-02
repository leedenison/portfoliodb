"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { SignInButton } from "@/app/components/sign-in";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import {
  getHoldings,
  getPortfolio,
  getPortfolioFilters,
  setPortfolioFilters,
} from "@/lib/portfolio-api";
import type { Portfolio, PortfolioFilter } from "@/lib/portfolio-api";
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

export default function PortfolioHoldingsPage() {
  const params = useParams();
  const id = typeof params?.id === "string" ? params.id : "";
  const { state, authError } = useAuth();
  const [portfolio, setPortfolio] = useState<Portfolio | null>(null);
  const [holdings, setHoldings] = useState<Awaited<ReturnType<typeof getHoldings>> | null>(null);
  const [filters, setFilters] = useState<PortfolioFilter[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filterSaving, setFilterSaving] = useState(false);
  const [newFilterType, setNewFilterType] = useState<string>("broker");
  const [newFilterValue, setNewFilterValue] = useState("");

  const fetchData = useCallback(async (portfolioId: string) => {
    setLoading(true);
    setError(null);
    try {
      const [port, h, f] = await Promise.all([
        getPortfolio(portfolioId),
        getHoldings({ portfolioId }),
        getPortfolioFilters(portfolioId),
      ]);
      setPortfolio(port);
      setHoldings(h);
      setFilters(f);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPortfolio(null);
      setHoldings(null);
      setFilters([]);
    } finally {
      setLoading(false);
    }
  }, []);

  const handleSaveFilters = useCallback(
    async (newFilters: PortfolioFilter[]) => {
      if (!id) return;
      setFilterSaving(true);
      try {
        await setPortfolioFilters(id, newFilters);
        setFilters(newFilters);
        const h = await getHoldings({ portfolioId: id });
        setHoldings(h);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setFilterSaving(false);
      }
    },
    [id]
  );

  const handleAddFilter = useCallback(() => {
    const t = newFilterType.trim();
    const v = newFilterValue.trim();
    if (!t) return;
    handleSaveFilters([...filters, { filterType: t, filterValue: v }]);
    setNewFilterValue("");
  }, [newFilterType, newFilterValue, filters, handleSaveFilters]);

  const handleRemoveFilter = useCallback(
    (index: number) => {
      const next = filters.filter((_, i) => i !== index);
      handleSaveFilters(next);
    },
    [filters, handleSaveFilters]
  );

  useEffect(() => {
    if (!id || state.status !== "authenticated") return;
    fetchData(id);
  }, [id, state.status, fetchData]);

  return (
    <AppShell>
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        {state.status === "loading" && (
          <p className="text-slate-500">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center">
            <h1 className="text-4xl font-bold tracking-tight text-slate-800">
              Portfolio holdings
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
          <div className="w-full max-w-2xl space-y-4">
            <Link
              href="/portfolios"
              className="text-sm text-slate-600 underline hover:text-slate-800"
            >
              Back to portfolios
            </Link>
            {loading && (
              <p className="text-slate-500">Loading holdings…</p>
            )}
            {!loading && error && (
              <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">
                {error}
              </p>
            )}
            {!loading && !error && portfolio && holdings && (
              <>
                <h2 className="text-xl font-semibold text-slate-800">
                  Holdings – {portfolio.name}
                </h2>
                {holdings.asOf && (
                  <p className="text-xs text-slate-500">
                    As of {holdings.asOf.toLocaleString()}
                  </p>
                )}
                <section className="rounded border border-slate-200 bg-slate-50 p-4">
                  <h3 className="mb-2 text-sm font-medium text-slate-700">
                    Portfolio view filters
                  </h3>
                  <p className="mb-3 text-xs text-slate-500">
                    This view shows transactions matching any of the filters below (e.g. broker, account, or instrument). Add filters to include transactions in this portfolio.
                  </p>
                  <ul className="mb-3 space-y-1 text-sm">
                    {filters.length === 0 ? (
                      <li className="text-slate-500">No filters. Add one below.</li>
                    ) : (
                      filters.map((f, i) => (
                        <li key={i} className="flex items-center gap-2">
                          <span className="font-medium text-slate-700">{f.filterType}</span>
                          <span className="text-slate-600">{f.filterValue || "(empty)"}</span>
                          <button
                            type="button"
                            onClick={() => handleRemoveFilter(i)}
                            disabled={filterSaving}
                            className="text-red-600 underline hover:no-underline disabled:opacity-50"
                          >
                            Remove
                          </button>
                        </li>
                      ))
                    )}
                  </ul>
                  <div className="flex flex-wrap items-center gap-2">
                    <select
                      value={newFilterType}
                      onChange={(e) => setNewFilterType(e.target.value)}
                      className="rounded border border-slate-300 bg-white px-2 py-1 text-sm"
                    >
                      <option value="broker">Broker</option>
                      <option value="account">Account</option>
                      <option value="instrument">Instrument (UUID)</option>
                    </select>
                    <input
                      type="text"
                      value={newFilterValue}
                      onChange={(e) => setNewFilterValue(e.target.value)}
                      placeholder="Value (e.g. IBKR or account name)"
                      className="min-w-[160px] rounded border border-slate-300 px-2 py-1 text-sm"
                    />
                    <button
                      type="button"
                      onClick={handleAddFilter}
                      disabled={filterSaving}
                      className="rounded bg-slate-700 px-3 py-1 text-sm text-white hover:bg-slate-800 disabled:opacity-50"
                    >
                      Add filter
                    </button>
                  </div>
                  {filterSaving && (
                    <p className="mt-2 text-xs text-slate-500">Saving…</p>
                  )}
                </section>
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
                            No holdings.
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
