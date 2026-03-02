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
        <h2 className="text-xl font-semibold text-slate-800">Your portfolios</h2>
        {!creating && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="rounded bg-slate-800 px-3 py-1.5 text-sm font-medium text-white hover:bg-slate-700"
          >
            New portfolio
          </button>
        )}
      </div>

      {creating && (
        <form onSubmit={handleCreate} className="flex flex-wrap items-center gap-2 rounded border border-slate-200 bg-white p-3">
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="Portfolio name"
            className="min-w-[200px] rounded border border-slate-300 px-2 py-1.5 text-sm"
            autoFocus
          />
          <button
            type="submit"
            className="rounded bg-slate-800 px-3 py-1.5 text-sm text-white hover:bg-slate-700"
          >
            Create
          </button>
          <button
            type="button"
            onClick={() => { setCreating(false); setNewName(""); }}
            className="rounded border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-100"
          >
            Cancel
          </button>
        </form>
      )}

      {error && (
        <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>
      )}

      {loading ? (
        <p className="text-slate-500">Loading portfolios…</p>
      ) : (
        <>
          <ul className="divide-y divide-slate-200 rounded border border-slate-200 bg-white">
            {portfolios.length === 0 && !creating ? (
              <li className="px-4 py-6 text-center text-slate-500">
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
                        className="min-w-0 flex-1 rounded border border-slate-300 px-2 py-1 text-sm"
                        autoFocus
                      />
                      <button
                        type="submit"
                        className="rounded bg-slate-800 px-2 py-1 text-sm text-white hover:bg-slate-700"
                      >
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => { setRenamingId(null); setRenameValue(""); }}
                        className="rounded border border-slate-300 px-2 py-1 text-sm hover:bg-slate-100"
                      >
                        Cancel
                      </button>
                    </form>
                  ) : deletingId === p.id ? (
                    <>
                      <span className="flex-1 text-sm text-slate-700">
                        Delete &quot;{p.name}&quot;?
                      </span>
                      <button
                        type="button"
                        onClick={() => handleDelete(p.id)}
                        className="rounded bg-red-600 px-2 py-1 text-sm text-white hover:bg-red-500"
                      >
                        Yes, delete
                      </button>
                      <button
                        type="button"
                        onClick={() => setDeletingId(null)}
                        className="rounded border border-slate-300 px-2 py-1 text-sm hover:bg-slate-100"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <div className="min-w-0 flex-1">
                        <Link
                          href={`/portfolios/${p.id}`}
                          className="font-medium text-slate-800 underline hover:text-slate-600"
                        >
                          {p.name}
                        </Link>
                        {p.createdAt && (
                          <span className="ml-2 text-xs text-slate-500">
                            {p.createdAt.toLocaleDateString()}
                          </span>
                        )}
                      </div>
                      <div className="flex shrink-0 gap-1">
                        <button
                          type="button"
                          onClick={() => startRename(p)}
                          className="rounded border border-slate-300 px-2 py-1 text-xs hover:bg-slate-100"
                        >
                          Rename
                        </button>
                        <button
                          type="button"
                          onClick={() => confirmDelete(p)}
                          className="rounded border border-red-200 px-2 py-1 text-xs text-red-700 hover:bg-red-50"
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
            <div className="flex items-center justify-between border-t border-slate-200 pt-2">
              <button
                type="button"
                onClick={goPrev}
                disabled={!hasPrev}
                className="rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 hover:enabled:bg-slate-100"
              >
                Previous
              </button>
              <span className="text-sm text-slate-500">
                Page {pageIndex + 1} (up to {PAGE_SIZE} per page)
              </span>
              <button
                type="button"
                onClick={goNext}
                disabled={!hasNext}
                className="rounded border border-slate-300 px-3 py-1.5 text-sm disabled:opacity-50 hover:enabled:bg-slate-100"
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
