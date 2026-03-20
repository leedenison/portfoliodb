"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { usePagination } from "@/hooks/use-pagination";
import {
  getPortfolioFilters,
  setPortfolioFilters,
  listBrokersAndAccounts,
  listInstruments,
} from "@/lib/portfolio-api";
import type { BrokerAccounts, PortfolioFilter } from "@/lib/portfolio-api";
import type { Instrument } from "@/gen/api/v1/api_pb";
import { IdentifierType } from "@/gen/api/v1/api_pb";

const ALL_ASSET_CLASSES = [
  "STOCK",
  "ETF",
  "OPTION",
  "FUTURE",
  "CASH",
  "MUTUAL_FUND",
  "FIXED_INCOME",
  "UNKNOWN",
] as const;

function displayName(inst: Instrument): string {
  const ticker = inst.identifiers.find(
    (id) => id.type === IdentifierType.TICKER
  );
  if (ticker) return ticker.value;
  if (inst.name) return inst.name;
  const desc = inst.identifiers.find(
    (id) => id.type === IdentifierType.BROKER_DESCRIPTION
  );
  if (desc) return desc.value;
  return inst.id;
}

interface Props {
  portfolioId: string;
  portfolioName: string;
  onDone: () => void;
}

export function PortfolioFilterEditor({
  portfolioId,
  portfolioName,
  onDone,
}: Props) {
  // Broker/account data from API.
  const [brokerAccounts, setBrokerAccounts] = useState<BrokerAccounts[]>([]);
  const [baLoading, setBaLoading] = useState(true);
  const [baError, setBaError] = useState<string | null>(null);

  // Selected filter values.
  const [selBrokers, setSelBrokers] = useState<Set<string>>(new Set());
  const [selAccounts, setSelAccounts] = useState<Set<string>>(new Set());
  const [selInstruments, setSelInstruments] = useState<
    Map<string, string>
  >(new Map()); // id -> display name

  // Instrument search state.
  const [instSearch, setInstSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [activeClasses, setActiveClasses] = useState<Set<string>>(
    () => new Set(ALL_ASSET_CLASSES)
  );
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  // Save state.
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [initialLoading, setInitialLoading] = useState(true);

  // Debounce instrument search.
  useEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setDebouncedSearch(instSearch), 300);
    return () => clearTimeout(debounceRef.current);
  }, [instSearch]);

  // Load existing filters and broker/accounts on mount.
  useEffect(() => {
    let cancelled = false;
    async function load() {
      setBaLoading(true);
      setBaError(null);
      try {
        const [ba, filters] = await Promise.all([
          listBrokersAndAccounts(),
          getPortfolioFilters(portfolioId),
        ]);
        if (cancelled) return;
        setBrokerAccounts(ba);
        const brokers = new Set<string>();
        const accounts = new Set<string>();
        const instruments = new Map<string, string>();
        for (const f of filters) {
          if (f.filterType === "broker") brokers.add(f.filterValue);
          else if (f.filterType === "account") accounts.add(f.filterValue);
          else if (f.filterType === "instrument")
            instruments.set(f.filterValue, f.filterValue);
        }
        setSelBrokers(brokers);
        setSelAccounts(accounts);
        setSelInstruments(instruments);
      } catch (e) {
        if (!cancelled) setBaError(e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) {
          setBaLoading(false);
          setInitialLoading(false);
        }
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [portfolioId]);

  // Resolve display names for instrument filters loaded from existing filters.
  // We only do this once after initial load to avoid refetching.
  const resolvedRef = useRef(false);
  useEffect(() => {
    if (initialLoading || resolvedRef.current) return;
    if (selInstruments.size === 0) return;
    // Check if any instrument IDs need name resolution (display name === id).
    const needsResolution = [...selInstruments.entries()].filter(
      ([id, name]) => id === name
    );
    if (needsResolution.length === 0) return;
    resolvedRef.current = true;
    // Search for each instrument by ID to get display names.
    Promise.all(
      needsResolution.map(([id]) =>
        listInstruments({ search: id, pageToken: null }).then((r) => {
          const match = r.instruments.find((i) => i.id === id);
          return match ? ([id, displayName(match)] as const) : null;
        })
      )
    ).then((results) => {
      setSelInstruments((prev) => {
        const next = new Map(prev);
        for (const r of results) {
          if (r) next.set(r[0], r[1]);
        }
        return next;
      });
    });
  }, [initialLoading, selInstruments]);

  const toggleBroker = (broker: string) => {
    setSelBrokers((prev) => {
      const next = new Set(prev);
      if (next.has(broker)) next.delete(broker);
      else next.add(broker);
      return next;
    });
  };

  const toggleAccount = (account: string) => {
    setSelAccounts((prev) => {
      const next = new Set(prev);
      if (next.has(account)) next.delete(account);
      else next.add(account);
      return next;
    });
  };

  const toggleInstrument = (inst: Instrument) => {
    setSelInstruments((prev) => {
      const next = new Map(prev);
      if (next.has(inst.id)) next.delete(inst.id);
      else next.set(inst.id, displayName(inst));
      return next;
    });
  };

  const removeInstrument = (id: string) => {
    setSelInstruments((prev) => {
      const next = new Map(prev);
      next.delete(id);
      return next;
    });
  };

  const toggleClass = (cls: string) => {
    setActiveClasses((prev) => {
      const next = new Set(prev);
      if (next.has(cls)) next.delete(cls);
      else next.add(cls);
      return next;
    });
  };

  const assetClassesKey = useMemo(
    () => [...activeClasses].sort().join(","),
    [activeClasses]
  );

  const fetchInstruments = useCallback(
    async (pageToken: string | null) => {
      const classes = assetClassesKey ? assetClassesKey.split(",") : [];
      const result = await listInstruments({
        search: debouncedSearch,
        assetClasses:
          classes.length < ALL_ASSET_CLASSES.length ? classes : [],
        pageToken,
      });
      return {
        items: result.instruments,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
    [debouncedSearch, assetClassesKey]
  );

  const {
    items: instruments,
    totalCount: instTotal,
    loading: instLoading,
    error: instError,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
  } = usePagination(fetchInstruments);

  const handleSave = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const filters: PortfolioFilter[] = [];
      for (const b of selBrokers)
        filters.push({ filterType: "broker", filterValue: b });
      for (const a of selAccounts)
        filters.push({ filterType: "account", filterValue: a });
      for (const [id] of selInstruments)
        filters.push({ filterType: "instrument", filterValue: id });
      await setPortfolioFilters(portfolioId, filters);
      onDone();
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  if (baLoading) {
    return (
      <div className="flex-1 px-5 py-8 text-center text-text-muted">
        Loading filters...
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      {/* Header */}
      <div className="flex items-center gap-2 border-b border-border px-5 py-3">
        <button
          type="button"
          onClick={onDone}
          className="rounded-md border border-border px-2 py-0.5 text-xs font-medium transition-colors hover:bg-primary-light/15"
        >
          Back
        </button>
        <span className="text-sm font-semibold text-text-primary">
          Filters: {portfolioName}
        </span>
      </div>

      {baError && (
        <div className="mx-5 mt-3">
          <ErrorAlert>{baError}</ErrorAlert>
        </div>
      )}
      {saveError && (
        <div className="mx-5 mt-3">
          <ErrorAlert>{saveError}</ErrorAlert>
        </div>
      )}

      <div className="flex-1 overflow-y-auto px-5 py-3 space-y-4">
        {/* Brokers */}
        <section>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-text-muted mb-1.5">
            Brokers
          </h3>
          {brokerAccounts.length === 0 ? (
            <p className="text-xs text-text-muted">
              No brokers found. Upload transactions first.
            </p>
          ) : (
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {brokerAccounts.map((ba) => (
                <label
                  key={ba.broker}
                  className="flex items-center gap-1.5 text-sm text-text-primary cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={selBrokers.has(ba.broker)}
                    onChange={() => toggleBroker(ba.broker)}
                    className="rounded border-border text-primary focus:ring-primary/30"
                  />
                  {ba.broker}
                </label>
              ))}
            </div>
          )}
        </section>

        {/* Accounts */}
        <section>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-text-muted mb-1.5">
            Accounts
          </h3>
          {brokerAccounts.length === 0 ? (
            <p className="text-xs text-text-muted">
              No accounts found. Upload transactions first.
            </p>
          ) : (
            <div className="space-y-2">
              {brokerAccounts.map((ba) => (
                <div key={ba.broker}>
                  <span className="text-xs font-medium text-text-muted">
                    {ba.broker}
                  </span>
                  <div className="flex flex-wrap gap-x-4 gap-y-1 ml-2 mt-0.5">
                    {ba.accounts.map((acct) => (
                      <label
                        key={`${ba.broker}:${acct}`}
                        className="flex items-center gap-1.5 text-sm text-text-primary cursor-pointer"
                      >
                        <input
                          type="checkbox"
                          checked={selAccounts.has(acct)}
                          onChange={() => toggleAccount(acct)}
                          className="rounded border-border text-primary focus:ring-primary/30"
                        />
                        {acct}
                      </label>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>

        {/* Instruments */}
        <section>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-text-muted mb-1.5">
            Instruments
          </h3>

          {/* Selected instruments as chips */}
          {selInstruments.size > 0 && (
            <div className="flex flex-wrap gap-1.5 mb-2">
              {[...selInstruments.entries()].map(([id, name]) => (
                <span
                  key={id}
                  className="inline-flex items-center gap-1 rounded-md bg-primary-dark/10 px-2 py-0.5 text-xs font-medium text-primary-dark"
                >
                  {name}
                  <button
                    type="button"
                    onClick={() => removeInstrument(id)}
                    className="ml-0.5 rounded-sm hover:bg-primary-dark/20"
                  >
                    <svg
                      className="h-3 w-3"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                      strokeWidth={2}
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        d="M6 18L18 6M6 6l12 12"
                      />
                    </svg>
                  </button>
                </span>
              ))}
            </div>
          )}

          {/* Search + asset class filters */}
          <div className="space-y-2">
            <input
              type="text"
              value={instSearch}
              onChange={(e) => setInstSearch(e.target.value)}
              placeholder="Search instruments..."
              className="w-full rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
            />
            <div className="flex flex-wrap gap-1">
              {ALL_ASSET_CLASSES.map((cls) => {
                const active = activeClasses.has(cls);
                return (
                  <button
                    key={cls}
                    type="button"
                    onClick={() => toggleClass(cls)}
                    className={
                      "rounded-md border px-2 py-0.5 text-xs font-medium transition-colors " +
                      (active
                        ? "border-primary bg-primary-dark/10 text-primary-dark"
                        : "border-border bg-surface text-text-muted hover:bg-primary-light/15")
                    }
                  >
                    {cls}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Instrument results */}
          {instLoading && (
            <p className="py-2 text-xs text-text-muted">
              Loading instruments...
            </p>
          )}
          {!instLoading && instError && <ErrorAlert>{instError}</ErrorAlert>}
          {!instLoading && !instError && (
            <div className="mt-2">
              {instruments.length === 0 ? (
                <p className="py-2 text-xs text-text-muted">
                  {debouncedSearch
                    ? "No instruments match your search."
                    : "No instruments found."}
                </p>
              ) : (
                <>
                  <div className="text-xs text-text-muted mb-1">
                    {instTotal} instruments
                  </div>
                  <ul className="divide-y divide-border/40 rounded-md border border-border">
                    {instruments.map((inst) => {
                      const checked = selInstruments.has(inst.id);
                      return (
                        <li
                          key={inst.id}
                          className="flex items-center gap-2 px-3 py-1.5 text-sm cursor-pointer hover:bg-primary-light/10"
                          onClick={() => toggleInstrument(inst)}
                        >
                          <input
                            type="checkbox"
                            checked={checked}
                            readOnly
                            className="rounded border-border text-primary focus:ring-primary/30"
                          />
                          <span className="flex-1 truncate text-text-primary">
                            {displayName(inst)}
                          </span>
                          <span className="shrink-0 text-xs text-text-muted">
                            {inst.assetClass || ""}
                          </span>
                        </li>
                      );
                    })}
                  </ul>
                  <PaginationControls
                    pageIndex={pageIndex}
                    hasPrev={hasPrev}
                    hasNext={hasNext}
                    onPrev={goPrev}
                    onNext={goNext}
                  />
                </>
              )}
            </div>
          )}
        </section>
      </div>

      {/* Footer: save/cancel */}
      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
        <button
          type="button"
          onClick={onDone}
          className="rounded-md border border-border px-3 py-1.5 text-sm font-medium hover:bg-primary-light/15"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-semibold text-white hover:bg-primary-dark disabled:opacity-50"
        >
          {saving ? "Saving..." : "Save filters"}
        </button>
      </div>
    </div>
  );
}
