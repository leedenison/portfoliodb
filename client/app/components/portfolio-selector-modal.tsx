"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { Modal } from "@/app/components/modal";
import { usePortfolio } from "@/contexts/portfolio-context";
import type { Portfolio } from "@/lib/portfolio-api";
import {
  listPortfolios,
  createPortfolio,
  updatePortfolio,
  deletePortfolio,
} from "@/lib/portfolio-api";

export function PortfolioSelectorModal() {
  const { selected, setSelected, modalOpen, closeModal } = usePortfolio();
  const [portfolios, setPortfolios] = useState<Portfolio[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deletingId, setDeletingId] = useState<string | null>(null);

  const fetchAll = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const all: Portfolio[] = [];
      let token: string | null = null;
      for (;;) {
        const result = await listPortfolios(token);
        all.push(...result.portfolios);
        if (!result.nextPageToken) break;
        token = result.nextPageToken;
      }
      setPortfolios(all);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (modalOpen) {
      fetchAll();
      setFilter("");
      setCreating(false);
      setNewName("");
      setRenamingId(null);
      setDeletingId(null);
    }
  }, [modalOpen, fetchAll]);

  const handleSelect = (p: Portfolio | null) => {
    setSelected(p);
    closeModal();
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newName.trim();
    if (!name) return;
    setError(null);
    try {
      await createPortfolio(name);
      setNewName("");
      setCreating(false);
      await fetchAll();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleRename = async (e: React.FormEvent, id: string) => {
    e.preventDefault();
    const name = renameValue.trim();
    if (!name) return;
    setError(null);
    try {
      await updatePortfolio(id, name);
      setRenamingId(null);
      // Update selected portfolio name if it was the one renamed.
      if (selected?.id === id) {
        setSelected({ ...selected, name });
      }
      await fetchAll();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleDelete = async (id: string) => {
    setError(null);
    try {
      await deletePortfolio(id);
      setDeletingId(null);
      // If the deleted portfolio was selected, revert to All Holdings.
      if (selected?.id === id) setSelected(null);
      await fetchAll();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const filtered = filter
    ? portfolios.filter((p) => p.name.toLowerCase().includes(filter.toLowerCase()))
    : portfolios;

  const isAllHoldings = selected === null;

  return (
    <Modal open={modalOpen} onClose={closeModal} title="Select portfolio">
      {/* Search + create */}
      <div className="flex items-center gap-2 border-b border-border px-5 py-3">
        <input
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter portfolios..."
          className="min-w-0 flex-1 rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
          autoFocus
        />
        {!creating && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="shrink-0 rounded-md bg-accent px-3 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
          >
            New
          </button>
        )}
      </div>

      {/* Create form */}
      {creating && (
        <form onSubmit={handleCreate} className="flex items-center gap-2 border-b border-border px-5 py-3">
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="Portfolio name"
            className="min-w-0 flex-1 rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
            autoFocus
          />
          <button type="submit" className="rounded-md bg-primary px-3 py-1.5 text-sm font-semibold text-white hover:bg-primary-dark">
            Create
          </button>
          <button
            type="button"
            onClick={() => { setCreating(false); setNewName(""); }}
            className="rounded-md border border-border px-3 py-1.5 text-sm hover:bg-primary-light/15"
          >
            Cancel
          </button>
        </form>
      )}

      {error && (
        <div className="mx-5 mt-3">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      {/* List */}
      <div className="flex-1 overflow-y-auto">
        {loading ? (
          <p className="px-5 py-8 text-center text-text-muted">Loading portfolios...</p>
        ) : (
          <ul className="divide-y divide-border/60">
            {/* All Holdings - always pinned at top */}
            <li
              className={
                "flex cursor-pointer items-center gap-3 px-5 py-3 transition-colors hover:bg-primary-light/10" +
                (isAllHoldings ? " bg-primary-dark/5" : "")
              }
              onClick={() => handleSelect(null)}
            >
              <span className="flex-1 text-sm font-semibold text-text-primary">All Holdings</span>
              {isAllHoldings && (
                <svg className="h-4 w-4 text-primary-dark" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              )}
            </li>

            {filtered.length === 0 && (
              <li className="px-5 py-6 text-center text-sm text-text-muted">
                {filter ? "No portfolios match your filter." : "No portfolios yet. Create one above."}
              </li>
            )}

            {filtered.map((p) => {
              const isSelected = selected?.id === p.id;
              return (
                <li
                  key={p.id}
                  className={
                    "flex items-center gap-2 px-5 py-3 transition-colors hover:bg-primary-light/10" +
                    (isSelected ? " bg-primary-dark/5" : "")
                  }
                >
                  {renamingId === p.id ? (
                    <form onSubmit={(e) => handleRename(e, p.id)} className="flex flex-1 items-center gap-2">
                      <input
                        type="text"
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        className="min-w-0 flex-1 rounded-md border border-border bg-surface px-2 py-1 text-sm text-text-primary focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
                        autoFocus
                      />
                      <button type="submit" className="rounded-md bg-primary px-2.5 py-1 text-sm font-semibold text-white hover:bg-primary-dark">
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={() => setRenamingId(null)}
                        className="rounded-md border border-border px-2.5 py-1 text-sm hover:bg-primary-light/15"
                      >
                        Cancel
                      </button>
                    </form>
                  ) : deletingId === p.id ? (
                    <>
                      <span className="flex-1 text-sm text-text-primary">Delete &quot;{p.name}&quot;?</span>
                      <button
                        type="button"
                        onClick={() => handleDelete(p.id)}
                        className="rounded-md bg-accent-dark px-2.5 py-1 text-sm font-semibold text-white hover:bg-accent"
                      >
                        Yes, delete
                      </button>
                      <button
                        type="button"
                        onClick={() => setDeletingId(null)}
                        className="rounded-md border border-border px-2.5 py-1 text-sm hover:bg-primary-light/15"
                      >
                        Cancel
                      </button>
                    </>
                  ) : (
                    <>
                      <button
                        type="button"
                        className="flex-1 cursor-pointer text-left text-sm font-medium text-text-primary"
                        onClick={() => handleSelect(p)}
                      >
                        {p.name}
                      </button>
                      {isSelected && (
                        <svg className="h-4 w-4 shrink-0 text-primary-dark" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                      <div className="flex shrink-0 gap-1">
                        <button
                          type="button"
                          onClick={(e) => { e.stopPropagation(); setRenamingId(p.id); setRenameValue(p.name); }}
                          className="rounded-md border border-border px-2 py-0.5 text-xs font-medium transition-colors hover:bg-primary-light/15"
                        >
                          Rename
                        </button>
                        <button
                          type="button"
                          onClick={(e) => { e.stopPropagation(); setDeletingId(p.id); }}
                          className="rounded-md border border-accent-soft px-2 py-0.5 text-xs font-medium text-accent-dark hover:bg-accent-soft/50"
                        >
                          Delete
                        </button>
                      </div>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </Modal>
  );
}
