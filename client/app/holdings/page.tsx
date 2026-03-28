"use client";

import { useCallback, useEffect, useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { usePortfolio } from "@/contexts/portfolio-context";
import { ErrorAlert } from "@/app/components/error-alert";
import { getHoldings } from "@/lib/portfolio-api";
import { getBrokerLabel } from "@/lib/csv/converters";
import { IdentifierType } from "@/gen/api/v1/api_pb";
import { OpeningBalances } from "./opening-balances";

type Tab = "holdings" | "opening-balances";

export default function UserHoldingsPage() {
  const { state, authError } = useAuth();
  const { selected: selectedPortfolio } = usePortfolio();
  const [holdings, setHoldings] = useState<Awaited<ReturnType<typeof getHoldings>> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<Tab>("holdings");

  const fetchData = useCallback(async (portfolioId?: string) => {
    setLoading(true);
    setError(null);
    try {
      const h = await getHoldings({ portfolioId });
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
    fetchData(selectedPortfolio?.id);
  }, [state.status, selectedPortfolio, fetchData]);

  return (
    <AppShell>
      <div data-testid="page-holdings" className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading...</p>
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
                {selectedPortfolio?.name ?? "All Holdings"}
              </h2>
              {activeTab === "holdings" && holdings?.asOf && (
                <span className="font-mono text-xs text-text-muted">
                  as of {holdings.asOf.toLocaleString()}
                </span>
              )}
            </div>

            <div className="flex gap-0 border-b border-border">
              <button
                type="button"
                onClick={() => setActiveTab("holdings")}
                className={
                  "px-4 py-2 text-sm font-medium transition-colors " +
                  (activeTab === "holdings"
                    ? "border-b-2 border-accent text-accent"
                    : "text-text-muted hover:text-text-primary")
                }
              >
                Holdings
              </button>
              <button
                type="button"
                onClick={() => setActiveTab("opening-balances")}
                className={
                  "px-4 py-2 text-sm font-medium transition-colors " +
                  (activeTab === "opening-balances"
                    ? "border-b-2 border-accent text-accent"
                    : "text-text-muted hover:text-text-primary")
                }
              >
                Opening Balances
              </button>
            </div>

            {activeTab === "holdings" && (
              <>
                {loading && (
                  <p className="text-text-muted">Loading holdings...</p>
                )}
                {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
                {!loading && !error && holdings && (
                  <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
                    <table data-testid="holdings-table" className="w-full min-w-[320px] border-collapse text-sm">
                      <thead>
                        <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                          <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                            Broker
                          </th>
                          <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                            Account
                          </th>
                          <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                            Exchange
                          </th>
                          <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                            Instrument
                          </th>
                          <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                            Quantity
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {holdings.holdings.length === 0 ? (
                          <tr>
                            <td
                              colSpan={5}
                              className="px-4 py-8 text-center text-text-muted"
                            >
                              No holdings. Upload transactions to get started.
                            </td>
                          </tr>
                        ) : (
                          holdings.holdings.map((h, i) => {
                            const ticker = h.instrument?.identifiers?.find(
                              (id) => id.type === IdentifierType.MIC_TICKER || id.type === IdentifierType.OPENFIGI_TICKER
                            )?.value;
                            return (
                              <tr
                                key={i}
                                className="border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
                              >
                                <td className="px-4 py-3 text-text-muted">
                                  {getBrokerLabel(h.broker)}
                                </td>
                                <td className="px-4 py-3 text-text-muted">
                                  {h.account || "\u2014"}
                                </td>
                                <td
                                  className="px-4 py-3 text-text-muted"
                                  title={h.instrument?.exchangeInfo?.name || ""}
                                >
                                  {h.instrument?.exchangeInfo?.acronym || h.instrument?.exchange || "\u2014"}
                                </td>
                                <td className="px-4 py-3 font-medium text-text-primary">
                                  {ticker || h.instrumentDescription || "\u2014"}
                                </td>
                                <td className="px-4 py-3 text-right font-mono tabular-nums text-text-primary">
                                  {h.quantity}
                                </td>
                              </tr>
                            );
                          })
                        )}
                      </tbody>
                    </table>
                  </div>
                )}
              </>
            )}

            {activeTab === "opening-balances" && <OpeningBalances />}
          </div>
        )}
      </div>
    </AppShell>
  );
}
