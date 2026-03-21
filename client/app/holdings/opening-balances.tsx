"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  listHoldingDeclarations,
  deleteHoldingDeclaration,
} from "@/lib/portfolio-api";
import { IdentifierType } from "@/gen/api/v1/api_pb";
import type { HoldingDeclaration } from "@/gen/api/v1/api_pb";
import { DeclarationForm } from "./declaration-form";

export function OpeningBalances() {
  const [declarations, setDeclarations] = useState<HoldingDeclaration[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editingDecl, setEditingDecl] = useState<HoldingDeclaration | null>(null);

  const fetchDeclarations = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const decls = await listHoldingDeclarations();
      setDeclarations(decls);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchDeclarations();
  }, [fetchDeclarations]);

  const handleDelete = async (id: string) => {
    try {
      await deleteHoldingDeclaration(id);
      fetchDeclarations();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleFormDone = () => {
    setShowForm(false);
    setEditingDecl(null);
    fetchDeclarations();
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
      {!loading && !error && (
        <>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={() => setShowForm(true)}
              className="rounded-md bg-accent px-3.5 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
            >
              Add checkpoint
            </button>
          </div>
          <p className="text-sm text-text-muted">
            Set checkpoints for known holding quantities at a point in time. The system will
            calculate an opening balance so that your records show this quantity on the date you specify.
          </p>
          <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
            <table className="w-full min-w-[480px] border-collapse text-sm">
              <thead>
                <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
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
                  <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody>
                {declarations.length === 0 ? (
                  <tr>
                    <td
                      colSpan={6}
                      className="px-4 py-8 text-center text-text-muted"
                    >
                      No opening balance checkpoints.
                    </td>
                  </tr>
                ) : (
                  declarations.map((d) => {
                    const ticker = d.instrument?.identifiers?.find(
                      (id) => id.type === IdentifierType.TICKER
                    )?.value;
                    return (
                      <tr
                        key={d.id}
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
                          {d.declaredQty}
                        </td>
                        <td className="px-4 py-3 text-text-muted">
                          {d.asOfDate}
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
