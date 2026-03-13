"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import {
  getHoldings,
  getPortfolio,
  getPortfolioFilters,
  setPortfolioFilters,
} from "@/lib/portfolio-api";
import type { Portfolio, PortfolioFilter } from "@/lib/portfolio-api";
import { getBrokerLabel } from "@/lib/csv/converters";

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
          <p className="text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Portfolio holdings
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
          <div className="w-full max-w-4xl animate-fade-in space-y-5">
            <Link
              href="/portfolios"
              className="inline-flex items-center gap-1 text-sm font-medium text-text-muted transition-colors hover:text-primary"
            >
              <span aria-hidden="true">&larr;</span> Portfolios
            </Link>
            {loading && (
              <p className="text-text-muted">Loading holdings…</p>
            )}
            {!loading && error && (
              <p className="rounded-md bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
                {error}
              </p>
            )}
            {!loading && !error && portfolio && holdings && (
              <>
                <div className="flex flex-wrap items-baseline gap-3">
                  <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                    {portfolio.name}
                  </h2>
                  {holdings.asOf && (
                    <span className="font-mono text-xs text-text-muted">
                      as of {holdings.asOf.toLocaleString()}
                    </span>
                  )}
                </div>
                <section className="rounded-md border border-primary-light/60 bg-primary-dark/[0.02] p-4">
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Filters
                  </h3>
                  <p className="mb-3 text-xs text-text-muted">
                    This view shows transactions matching any of the filters below (e.g. broker, account, or instrument). Add filters to include transactions in this portfolio.
                  </p>
                  <ul className="mb-3 space-y-1.5 text-sm">
                    {filters.length === 0 ? (
                      <li className="text-text-muted">No filters. Add one below.</li>
                    ) : (
                      filters.map((f, i) => (
                        <li key={i} className="flex items-center gap-2">
                          <span className="rounded bg-primary-dark/10 px-1.5 py-0.5 font-mono text-xs font-medium text-primary-dark">{f.filterType}</span>
                          <span className="text-text-muted">{f.filterValue || "(empty)"}</span>
                          <button
                            type="button"
                            onClick={() => handleRemoveFilter(i)}
                            disabled={filterSaving}
                            className="text-xs text-accent-dark underline hover:no-underline disabled:opacity-50"
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
                      className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-none"
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
                      className="min-w-[160px] rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none"
                    />
                    <button
                      type="button"
                      onClick={handleAddFilter}
                      disabled={filterSaving}
                      className="rounded-md bg-primary px-3 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-primary-dark disabled:opacity-50"
                    >
                      Add filter
                    </button>
                  </div>
                  {filterSaving && (
                    <p className="mt-2 text-xs text-text-muted">Saving…</p>
                  )}
                </section>
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
                            No holdings.
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
              </>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}
