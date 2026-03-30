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
  getHoldings,
} from "@/lib/portfolio-api";
import type { BrokerAccounts, PortfolioFilter } from "@/lib/portfolio-api";
import type { Instrument } from "@/gen/api/v1/api_pb";

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

type Tab = "accounts" | "instruments";

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
  const [tab, setTab] = useState<Tab>("accounts");

  // Broker/account data from API.
  const [brokerAccounts, setBrokerAccounts] = useState<BrokerAccounts[]>([]);
  const [baLoading, setBaLoading] = useState(true);
  const [baError, setBaError] = useState<string | null>(null);

  // Selected filter values.
  const [selBrokers, setSelBrokers] = useState<Set<string>>(new Set());
  const [selAccounts, setSelAccounts] = useState<Set<string>>(new Set());
  const [selInstruments, setSelInstruments] = useState<Map<string, string>>(
    new Map()
  ); // id -> display name

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

  // Debounce instrument search.
  useEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => setDebouncedSearch(instSearch), 300);
    return () => clearTimeout(debounceRef.current);
  }, [instSearch]);

  // Load existing filters, broker/accounts, and resolve instrument names on mount.
  useEffect(() => {
    let cancelled = false;
    async function load() {
      setBaLoading(true);
      setBaError(null);
      try {
        const [ba, filters, holdingsResult] = await Promise.all([
          listBrokersAndAccounts(),
          getPortfolioFilters(portfolioId),
          getHoldings({ portfolioId }),
        ]);
        if (cancelled) return;
        setBrokerAccounts(ba);

        // Build instrument ID -> display name lookup from holdings.
        const instNames = new Map<string, string>();
        for (const h of holdingsResult.holdings) {
          if (h.instrumentId && h.instrument) {
            instNames.set(h.instrumentId, h.instrument.name || h.instrumentId);
          }
        }

        const brokers = new Set<string>();
        const accounts = new Set<string>();
        const instruments = new Map<string, string>();
        for (const f of filters) {
          if (f.filterType === "broker") brokers.add(f.filterValue);
          else if (f.filterType === "account") accounts.add(f.filterValue);
          else if (f.filterType === "instrument") {
            const name = instNames.get(f.filterValue) ?? f.filterValue;
            instruments.set(f.filterValue, name);
          }
        }
        setSelBrokers(brokers);
        setSelAccounts(accounts);
        setSelInstruments(instruments);
      } catch (e) {
        if (!cancelled) setBaError(e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) {
          setBaLoading(false);
        }
      }
    }
    load();
    return () => {
      cancelled = true;
    };
  }, [portfolioId]);

  // Build a lookup: account -> broker, for auto-selecting parent broker.
  const accountToBroker = useMemo(() => {
    const m = new Map<string, string>();
    for (const ba of brokerAccounts) {
      for (const acct of ba.accounts) {
        m.set(acct, ba.broker);
      }
    }
    return m;
  }, [brokerAccounts]);

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
      if (next.has(account)) {
        next.delete(account);
      } else {
        next.add(account);
        // Auto-select parent broker when selecting an account.
        const broker = accountToBroker.get(account);
        if (broker && !selBrokers.has(broker)) {
          setSelBrokers((bp) => new Set(bp).add(broker));
        }
      }
      return next;
    });
  };

  const toggleInstrument = (inst: Instrument) => {
    setSelInstruments((prev) => {
      const next = new Map(prev);
      if (next.has(inst.id)) next.delete(inst.id);
      else next.set(inst.id, (inst.name || inst.id));
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

      {(baError || saveError) && (
        <div className="mx-5 mt-3">
          <ErrorAlert>{baError || saveError}</ErrorAlert>
        </div>
      )}

      {/* Tabs */}
      <div className="flex border-b border-border">
        {(["accounts", "instruments"] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={
              "flex-1 px-4 py-2.5 text-sm font-medium transition-colors " +
              (tab === t
                ? "border-b-2 border-primary text-primary-dark"
                : "text-text-muted hover:text-text-primary hover:bg-primary-light/10")
            }
          >
            {t === "accounts" ? "Accounts" : "Instruments"}
            {t === "accounts" && (selBrokers.size > 0 || selAccounts.size > 0) && (
              <span className="ml-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-dark/15 px-1 text-[10px] font-semibold text-primary-dark">
                {selBrokers.size + selAccounts.size}
              </span>
            )}
            {t === "instruments" && selInstruments.size > 0 && (
              <span className="ml-1.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-dark/15 px-1 text-[10px] font-semibold text-primary-dark">
                {selInstruments.size}
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="flex-1 overflow-y-auto px-5 py-3">
        {tab === "accounts" ? (
          <AccountsTab
            brokerAccounts={brokerAccounts}
            selBrokers={selBrokers}
            selAccounts={selAccounts}
            onToggleBroker={toggleBroker}
            onToggleAccount={toggleAccount}
          />
        ) : (
          <InstrumentsTab
            selInstruments={selInstruments}
            instSearch={instSearch}
            onSearchChange={setInstSearch}
            activeClasses={activeClasses}
            onToggleClass={toggleClass}
            instruments={instruments}
            instTotal={instTotal}
            instLoading={instLoading}
            instError={instError}
            debouncedSearch={debouncedSearch}
            pageIndex={pageIndex}
            hasPrev={hasPrev}
            hasNext={hasNext}
            goNext={goNext}
            goPrev={goPrev}
            onToggleInstrument={toggleInstrument}
            onRemoveInstrument={removeInstrument}
          />
        )}
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

// --- Accounts tab: broker/account tree ---

function AccountsTab({
  brokerAccounts,
  selBrokers,
  selAccounts,
  onToggleBroker,
  onToggleAccount,
}: {
  brokerAccounts: BrokerAccounts[];
  selBrokers: Set<string>;
  selAccounts: Set<string>;
  onToggleBroker: (broker: string) => void;
  onToggleAccount: (account: string) => void;
}) {
  if (brokerAccounts.length === 0) {
    return (
      <p className="py-4 text-sm text-text-muted">
        No brokers or accounts found. Upload transactions first.
      </p>
    );
  }

  return (
    <div className="space-y-1">
      {brokerAccounts.map((ba) => {
        const brokerChecked = selBrokers.has(ba.broker);
        return (
          <div key={ba.broker}>
            {/* Broker (parent) */}
            <label className="flex items-center gap-2 rounded-md px-2 py-1.5 cursor-pointer hover:bg-primary-light/10">
              <input
                type="checkbox"
                checked={brokerChecked}
                onChange={() => onToggleBroker(ba.broker)}
                className="rounded border-border text-primary focus:ring-primary/30"
              />
              <span className="text-sm font-semibold text-text-primary">
                {ba.broker}
              </span>
            </label>
            {/* Accounts (children) */}
            <div className="ml-6 space-y-0.5">
              {ba.accounts.map((acct) => (
                <label
                  key={`${ba.broker}:${acct}`}
                  className="flex items-center gap-2 rounded-md px-2 py-1 cursor-pointer hover:bg-primary-light/10"
                >
                  <input
                    type="checkbox"
                    checked={selAccounts.has(acct)}
                    onChange={() => onToggleAccount(acct)}
                    className="rounded border-border text-primary focus:ring-primary/30"
                  />
                  <span className="text-sm text-text-primary">{acct}</span>
                </label>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// --- Instruments tab ---

function InstrumentsTab({
  selInstruments,
  instSearch,
  onSearchChange,
  activeClasses,
  onToggleClass,
  instruments,
  instTotal,
  instLoading,
  instError,
  debouncedSearch,
  pageIndex,
  hasPrev,
  hasNext,
  goNext,
  goPrev,
  onToggleInstrument,
  onRemoveInstrument,
}: {
  selInstruments: Map<string, string>;
  instSearch: string;
  onSearchChange: (v: string) => void;
  activeClasses: Set<string>;
  onToggleClass: (cls: string) => void;
  instruments: Instrument[];
  instTotal: number;
  instLoading: boolean;
  instError: string | null;
  debouncedSearch: string;
  pageIndex: number;
  hasPrev: boolean;
  hasNext: boolean;
  goNext: () => void;
  goPrev: () => void;
  onToggleInstrument: (inst: Instrument) => void;
  onRemoveInstrument: (id: string) => void;
}) {
  return (
    <div className="space-y-3">
      {/* Selected instruments as chips */}
      {selInstruments.size > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {[...selInstruments.entries()].map(([id, name]) => (
            <span
              key={id}
              className="inline-flex items-center gap-1 rounded-md bg-primary-dark/10 px-2 py-0.5 text-xs font-medium text-primary-dark"
            >
              {name}
              <button
                type="button"
                onClick={() => onRemoveInstrument(id)}
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
          onChange={(e) => onSearchChange(e.target.value)}
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
                onClick={() => onToggleClass(cls)}
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
        <p className="py-2 text-xs text-text-muted">Loading instruments...</p>
      )}
      {!instLoading && instError && <ErrorAlert>{instError}</ErrorAlert>}
      {!instLoading && !instError && (
        <div>
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
                      onClick={() => onToggleInstrument(inst)}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        readOnly
                        className="rounded border-border text-primary focus:ring-primary/30"
                      />
                      <span className="flex-1 truncate text-text-primary">
                        {(inst.name || inst.id)}
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
    </div>
  );
}
