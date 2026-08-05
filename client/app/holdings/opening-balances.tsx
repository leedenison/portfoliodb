"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ErrorAlert } from "@/app/components/error-alert";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { usePortfolio } from "@/contexts/portfolio-context";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import {
  listHoldingDeclarations,
  deleteHoldingDeclaration,
  listBrokersAndAccounts,
  protoDateToStr,
} from "@/lib/portfolio-api";
import { DeclarationKind, IdentifierType } from "@/gen/api/v1/api_pb";
import type { HoldingDeclaration } from "@/gen/api/v1/api_pb";
import { DeclarationForm } from "./declaration-form";

/**
 * Which share count the declared quantity is in, said the way the form asks it. The
 * denomination only matters once it differs from the as of date, so a declaration on
 * the as-traded default reads as the date itself rather than as jargon.
 */
function shareCountLabel(decl: HoldingDeclaration): string {
  const basis = decl.shareCountBasis ? protoDateToStr(decl.shareCountBasis) : "";
  const asOf = decl.asOfDate ? protoDateToStr(decl.asOfDate) : "";
  if (!basis || basis === asOf) return "As of date";
  return `As of ${basis}`;
}

/**
 * What the check says about a declaration. A pad is true by construction, so it
 * reports what it is rather than that it passed -- calling it "matches" would suggest
 * it had found something out. An assertion that disagrees says by how much, because
 * the size of the gap is what identifies the missing transaction.
 */
function checkStatus(decl: HoldingDeclaration): { label: string; alert: boolean } {
  if (decl.kind === DeclarationKind.PAD) return { label: "Opening balance", alert: false };
  if (!decl.computedQty) return { label: "—", alert: false };
  if (decl.matched) return { label: "Matches", alert: false };
  const delta = Number(decl.delta);
  const sign = delta > 0 ? "+" : "";
  return { label: `Off by ${sign}${parseFloat(delta.toFixed(4))}`, alert: true };
}

export function OpeningBalances() {
  const queryClient = useQueryClient();
  const { selected: selectedPortfolio } = usePortfolio();
  const portfolioId = selectedPortfolio?.id;
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editingDecl, setEditingDecl] = useState<HoldingDeclaration | null>(null);

  // Two independent lists rather than the Promise.all they used to share: the
  // broker list is also read by the declaration form, so keeping it separate
  // means the two share a cache entry.
  const {
    data: declarations = [],
    isPending: declLoading,
    error: declError,
  } = useAuthedQuery<HoldingDeclaration[]>({
    queryKey: qk.holdingDeclarations(),
    queryFn: listHoldingDeclarations,
  });
  const {
    data: brokers = [],
    isPending: brokersLoading,
    error: brokersError,
  } = useAuthedQuery<Awaited<ReturnType<typeof listBrokersAndAccounts>>>({
    queryKey: qk.brokersAndAccounts(),
    queryFn: listBrokersAndAccounts,
  });

  const hasBrokers = brokers.length > 0;
  const loading = declLoading || brokersLoading;
  const loadError = declError ?? brokersError;
  const error = deleteError ?? (loadError ? errorMessage(loadError) : null);

  // Writing a declaration writes the pad it derives, which is a posting -- so the
  // holdings the sibling tab shows move with it. Invalidating only the declaration
  // list left that tab reading the figure from before the opening balance existed,
  // because it shares this page and never remounts. Every other view of the pad is
  // on a page of its own and refetches when it mounts.
  const refreshDeclarations = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: qk.holdingDeclarations() }),
      queryClient.invalidateQueries({ queryKey: qk.holdings(portfolioId) }),
    ]);
  };

  const handleDelete = async (id: string) => {
    setDeleteError(null);
    try {
      await deleteHoldingDeclaration(id);
      await refreshDeclarations();
    } catch (e) {
      setDeleteError(errorMessage(e));
    }
  };

  const handleFormDone = () => {
    setShowForm(false);
    setEditingDecl(null);
    void refreshDeclarations();
  };

  if (showForm || editingDecl) {
    return (
      <DeclarationForm
        editing={editingDecl}
        onDone={handleFormDone}
        onCancel={() => { setShowForm(false); setEditingDecl(null); }}
      />
    );
  }

  return (
    <>
      {loading && <p className="text-text-muted">Loading checkpoints...</p>}
      {!loading && error && <ErrorAlert>{error}</ErrorAlert>}
      {!loading && !error && !hasBrokers && (
        <p className="text-sm text-text-muted">
          There are no transactions. Upload some transactions in order to create an opening balance.
        </p>
      )}
      {!loading && !error && hasBrokers && (
        <>
          <div className="flex justify-end">
            <button
              type="button"
              data-testid="add-declaration"
              onClick={() => setShowForm(true)}
              className="rounded-md bg-accent px-3.5 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
            >
              Add checkpoint
            </button>
          </div>
          <p className="text-sm text-text-muted">
            Set checkpoints for known holding quantities at a point in time. The earliest
            checkpoint for a holding becomes its opening balance: the system calculates one so
            that your records show this quantity on the date you specify. Later checkpoints are
            checked against what your transactions add up to, and a disagreement means a
            transaction is missing or wrong.
          </p>
          <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-xs">
            <table data-testid="declarations-table" className="w-full min-w-[480px] border-collapse text-sm">
              <thead>
                <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/3">
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Broker
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Account
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Instrument
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Units Held
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    As Of Date
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Share Count
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Status
                  </th>
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody>
                {declarations.length === 0 ? (
                  <tr>
                    <td
                      colSpan={8}
                      className="px-4 py-8 text-center text-text-muted"
                    >
                      No opening balance checkpoints.
                    </td>
                  </tr>
                ) : (
                  declarations.map((d) => {
                    const ticker = d.instrument?.identifiers?.find(
                      (id) => id.type === IdentifierType.MIC_TICKER || id.type === IdentifierType.OPENFIGI_TICKER
                    )?.value;
                    return (
                      <tr
                        key={d.id}
                        data-testid="declaration-row"
                        className="border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
                      >
                        <td className="px-4 py-3 text-text-muted">
                          {d.broker}
                        </td>
                        <td className="px-4 py-3 text-text-muted">
                          {d.account || "\u2014"}
                        </td>
                        <td className="px-4 py-3 font-medium text-text-primary">
                          {ticker || d.instrument?.name || d.instrumentId}
                        </td>
                        <td className="px-4 py-3 text-right font-mono tabular-nums text-text-primary">
                          {parseFloat(Number(d.declaredQty).toFixed(4))}
                        </td>
                        <td className="px-4 py-3 text-text-muted">
                          {protoDateToStr(d.asOfDate)}
                        </td>
                        <td className="px-4 py-3 text-text-muted">
                          {shareCountLabel(d)}
                        </td>
                        <td
                          className={
                            checkStatus(d).alert
                              ? "px-4 py-3 font-medium text-amber-700 dark:text-amber-400"
                              : "px-4 py-3 text-text-muted"
                          }
                          data-testid="declaration-status"
                        >
                          {checkStatus(d).label}
                        </td>
                        <td className="px-4 py-3 text-right">
                          <button
                            type="button"
                            onClick={() => setEditingDecl(d)}
                            className="mr-2 text-sm text-primary-dark hover:underline"
                          >
                            Edit
                          </button>
                          <button
                            type="button"
                            onClick={() => handleDelete(d.id)}
                            className="text-sm text-accent-dark hover:underline"
                          >
                            Delete
                          </button>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  );
}
