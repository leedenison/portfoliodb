"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import type { Portfolio } from "@/lib/portfolio-api";
import {
  listPortfolios,
  createPortfolio,
  updatePortfolio,
  deletePortfolio,
} from "@/lib/portfolio-api";

const PAGE_SIZE = 30;

export function PortfolioList() {
  const [portfolios, setPortfolios] = useState<Portfolio[]>([]);
  const [nextPageToken, setNextPageToken] = useState<string | null>(null);
  const [pageTokens, setPageTokens] = useState<(string | null)[]>([null]);
  const [pageIndex, setPageIndex] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const fetchPage = useCallback(async (pageToken: string | null, forPageIndex: number) => {
    setLoading(true);
    setError(null);
    try {
      const result = await listPortfolios(pageToken);
      setPortfolios(result.portfolios);
      setNextPageToken(result.nextPageToken);
      if (result.nextPageToken != null && result.nextPageToken !== "") {
        setPageTokens((prev) => {
          const next = [...prev];
          next[forPageIndex + 1] = result.nextPageToken!;
          return next;
        });
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPortfolios([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const token = pageTokens[pageIndex] ?? null;
    fetchPage(token, pageIndex);
  }, [pageIndex, fetchPage]);

  const refetchCurrent = useCallback(() => {
    const token = pageTokens[pageIndex] ?? null;
    fetchPage(token, pageIndex);
  }, [pageIndex, pageTokens, fetchPage]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    setError(null);
    try {
      await createPortfolio(name);
      setNewName("");
      setCreating(false);
      if (pageIndex === 0) refetchCurrent();
      else {
        setPageIndex(0);
        setPageTokens([null]);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const startRename = (p: Portfolio) => {
    setRenamingId(p.id);
    setRenameValue(p.name);
  };

  const handleRename = async (e: React.FormEvent, id: string) => {
    e.preventDefault();
    const name = renameValue.trim();
    if (!name) return;
    setError(null);
    try {
      await updatePortfolio(id, name);
      setRenamingId(null);
      refetchCurrent();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const confirmDelete = (p: Portfolio) => {
    setDeletingId(p.id);
  };

  const handleDelete = async (id: string) => {
    setError(null);
    try {
      await deletePortfolio(id);
      setDeletingId(null);
      refetchCurrent();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const goNext = () => {
    if (nextPageToken) setPageIndex((i) => i + 1);
  };

  const goPrev = () => {
    if (pageIndex > 0) setPageIndex((i) => i - 1);
  };

  const hasPrev = pageIndex > 0;
  const hasNext = !!nextPageToken;

  return (
    <div className="w-full max-w-2xl space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-text-primary">Your portfolios</h2>
        {!creating && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-accent-dark"
          >
            New portfolio
          </button>
        )}
      </div>

      {creating && (
        <form onSubmit={handleCreate} className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-surface p-3 shadow-sm">
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="Portfolio name"
            className="min-w-[200px] rounded-lg border border-border bg-surface px-2 py-1.5 text-sm text-text-primary placeholder:text-text-muted"
            autoFocus
          />
          <button
            type="submit"
            className="rounded-lg bg-primary px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-primary-dark"
          >
            Create
          </button>
          <button
            type="button"
            onClick={() => { setCreating(false); setNewName(""); }}
            className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-text-primary transition-colors hover:bg-primary-light/20"
          >
            Cancel
          </button>
        </form>
      )}

      {error && (
        <p className="rounded-lg bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">{error}</p>
      )}

      {loading ? (
        <p className="text-text-muted">Loading portfolios…</p>
      ) : (
        <>
          <ul className="divide-y divide-border rounded-lg border border-border bg-surface shadow-sm">
            {portfolios.length === 0 && !creating ? (
              <li className="px-4 py-6 text-center text-text-muted">
                No portfolios yet. Create one above.
              </li>
            ) : (
              portfolios.map((p) => (
                <li key={p.id} className="flex items-center justify-between gap-2 px-4 py-3">
                  {renamingId === p.id ? (
                    <form
                      onSubmit={(e) => handleRename(e, p.id)}
                      className="flex flex-1 items-center gap-2"
                    >
                      <input
                        type="text"
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        className="min-w-0 flex-1 rounded-lg border border-border bg-surface px-2 py-1 text-sm text-text-primary"
                        autoFocus
                      />
                      <button
                        type="submit"
                        className="rounded-lg bg-primary px-2 py-1 text-sm font-medium text-white hover:bg-primary-dark"
                      >
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => { setRenamingId(null); setRenameValue(""); }}
                        className="rounded-lg border border-border px-2 py-1 text-sm hover:bg-primary-light/20"
                      >
                        Cancel
                      </button>
                    </form>
                  ) : deletingId === p.id ? (
                    <>
                      <span className="flex-1 text-sm text-text-primary">
                        Delete &quot;{p.name}&quot;?
                      </span>
                      <button
                        type="button"
                        onClick={() => handleDelete(p.id)}
                        className="rounded-lg bg-accent-dark px-2 py-1 text-sm font-medium text-white hover:bg-accent"
                      >
                        Yes, delete
                      </button>
                      <button
                        type="button"
                        onClick={() => setDeletingId(null)}
                        className="rounded-lg border border-border px-2 py-1 text-sm hover:bg-primary-light/20"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <div className="min-w-0 flex-1">
                        <Link
                          href={`/portfolios/${p.id}`}
                          className="font-medium text-primary underline transition-colors hover:text-primary-dark"
                        >
                          {p.name}
                        </Link>
                        {p.createdAt && (
                          <span className="ml-2 text-xs text-text-muted">
                            {p.createdAt.toLocaleDateString()}
                          </span>
                        )}
                      </div>
                      <div className="flex shrink-0 gap-1">
                        <button
                          type="button"
                          onClick={() => startRename(p)}
                          className="rounded-lg border border-border px-2 py-1 text-xs transition-colors hover:bg-primary-light/20"
                        >
                          Rename
                        </button>
                        <button
                          type="button"
                          onClick={() => confirmDelete(p)}
                          className="rounded-lg border border-accent-soft px-2 py-1 text-xs text-accent-dark hover:bg-accent-soft/50"
                        >
                          Delete
                        </button>
                      </div>
                    </>
                  )}
                </li>
              ))
            )}
          </ul>

          {(hasPrev || hasNext) && (
            <div className="flex items-center justify-between border-t border-border pt-2">
              <button
                type="button"
                onClick={goPrev}
                disabled={!hasPrev}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm disabled:opacity-50 hover:enabled:bg-primary-light/20"
              >
                Previous
              </button>
              <span className="text-sm text-text-muted">
                Page {pageIndex + 1} (up to {PAGE_SIZE} per page)
              </span>
              <button
                type="button"
                onClick={goNext}
                disabled={!hasNext}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm disabled:opacity-50 hover:enabled:bg-primary-light/20"
              >
                Next
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
