"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { listInstruments } from "@/lib/portfolio-api";
import { IdentifierType } from "@/gen/api/v1/api_pb";
import type { Instrument, InstrumentIdentifier } from "@/gen/api/v1/api_pb";

const IDENTIFIER_LABELS: Record<number, string> = {
  [IdentifierType.ISIN]: "ISIN",
  [IdentifierType.CUSIP]: "CUSIP",
  [IdentifierType.SEDOL]: "SEDOL",
  [IdentifierType.CINS]: "CINS",
  [IdentifierType.WERTPAPIER]: "Wertpapier",
  [IdentifierType.OCC]: "OCC",
  [IdentifierType.OPRA]: "OPRA",
  [IdentifierType.FUT_OPT]: "Fut/Opt",
  [IdentifierType.OPENFIGI_GLOBAL]: "FIGI Global",
  [IdentifierType.OPENFIGI_SHARE_CLASS]: "FIGI Share",
  [IdentifierType.OPENFIGI_COMPOSITE]: "FIGI Composite",
  [IdentifierType.TICKER]: "Ticker",
  [IdentifierType.BROKER_DESCRIPTION]: "Broker Desc",
  [IdentifierType.CURRENCY]: "Currency",
};

function idLabel(id: InstrumentIdentifier): string {
  return IDENTIFIER_LABELS[id.type] ?? String(id.type);
}

function displayName(inst: Instrument): string {
  const ticker = inst.identifiers.find(
    (id) => id.type === IdentifierType.TICKER
  );
  if (ticker) return ticker.value;
  const desc = inst.identifiers.find(
    (id) => id.type === IdentifierType.BROKER_DESCRIPTION
  );
  if (desc) return desc.value;
  return inst.name || inst.id;
}

function isIdentified(inst: Instrument): boolean {
  return inst.identifiers.some((id) => id.canonical);
}

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

const DEFAULT_ASSET_CLASSES = new Set(["STOCK", "ETF", "OPTION", "FUTURE"]);

export default function InstrumentsPage() {
  const { state, authError } = useAuth();
  const [instruments, setInstruments] = useState<Instrument[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [activeClasses, setActiveClasses] = useState<Set<string>>(
    () => new Set(DEFAULT_ASSET_CLASSES)
  );
  const [nextPageToken, setNextPageToken] = useState<string | null>(null);
  const [pageTokens, setPageTokens] = useState<(string | null)[]>([null]);
  const [pageIndex, setPageIndex] = useState(0);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();

  // Debounce search input.
  useEffect(() => {
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setDebouncedSearch(search);
      setPageIndex(0);
      setPageTokens([null]);
      setExpandedId(null);
    }, 300);
    return () => clearTimeout(debounceRef.current);
  }, [search]);

  const toggleClass = (cls: string) => {
    setActiveClasses((prev) => {
      const next = new Set(prev);
      if (next.has(cls)) next.delete(cls);
      else next.add(cls);
      return next;
    });
    setPageIndex(0);
    setPageTokens([null]);
    setExpandedId(null);
  };

  // Memoize the sorted array so the effect dep is stable when the set contents haven't changed.
  const assetClassesKey = [...activeClasses].sort().join(",");

  const fetchPage = useCallback(
    async (
      pageToken: string | null,
      forPageIndex: number,
      searchTerm: string,
      classes: string[]
    ) => {
      setLoading(true);
      setError(null);
      try {
        const result = await listInstruments({
          search: searchTerm,
          assetClasses: classes.length < ALL_ASSET_CLASSES.length ? classes : [],
          pageToken,
        });
        setInstruments(result.instruments);
        setTotalCount(result.totalCount);
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
        setInstruments([]);
        setTotalCount(0);
      } finally {
        setLoading(false);
      }
    },
    []
  );

  useEffect(() => {
    if (state.status !== "authenticated") return;
    const token = pageTokens[pageIndex] ?? null;
    const classes = assetClassesKey ? assetClassesKey.split(",") : [];
    fetchPage(token, pageIndex, debouncedSearch, classes);
  }, [state.status, pageIndex, debouncedSearch, assetClassesKey, fetchPage]);

  const goNext = () => {
    if (nextPageToken) setPageIndex((i) => i + 1);
  };
  const goPrev = () => {
    if (pageIndex > 0) setPageIndex((i) => i - 1);
  };
  const hasPrev = pageIndex > 0;
  const hasNext = !!nextPageToken;

  return (
    <AppShell>
      <div className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Instruments
            </h1>
            <p className="mt-3 text-text-muted">
              Sign in to browse instruments.
            </p>
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
                Instruments
              </h2>
              {!loading && (
                <span className="font-mono text-xs text-text-muted">
                  {totalCount} total
                </span>
              )}
            </div>

            {/* Search and filters */}
            <div className="space-y-3">
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search by identifier…"
                className="w-full max-w-sm rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/30"
              />
              <div className="flex flex-wrap gap-1.5">
                {ALL_ASSET_CLASSES.map((cls) => {
                  const active = activeClasses.has(cls);
                  return (
                    <button
                      key={cls}
                      type="button"
                      onClick={() => toggleClass(cls)}
                      className={
                        "rounded-md border px-2.5 py-1 text-xs font-medium transition-colors " +
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

            {loading && (
              <p className="text-text-muted">Loading instruments…</p>
            )}
            {!loading && error && (
              <p className="rounded-md bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
                {error}
              </p>
            )}
            {!loading && !error && (
              <>
                <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-sm">
                  <table className="w-full min-w-[480px] border-collapse text-sm">
                    <thead>
                      <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/[0.03]">
                        <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                          Name
                        </th>
                        <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                          Asset Class
                        </th>
                        <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                          Exchange
                        </th>
                        <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                          Currency
                        </th>
                        <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                          Status
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {instruments.length === 0 ? (
                        <tr>
                          <td
                            colSpan={5}
                            className="px-4 py-8 text-center text-text-muted"
                          >
                            {debouncedSearch
                              ? "No instruments match your search."
                              : "No instruments in the system yet."}
                          </td>
                        </tr>
                      ) : (
                        instruments.map((inst) => {
                          const identified = isIdentified(inst);
                          const expanded = expandedId === inst.id;
                          return (
                            <tr
                              key={inst.id}
                              className="group cursor-pointer border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
                              onClick={() =>
                                setExpandedId(expanded ? null : inst.id)
                              }
                            >
                              <td
                                className="px-4 py-3 font-medium text-text-primary"
                                colSpan={expanded ? 5 : 1}
                              >
                                {expanded ? (
                                  <ExpandedDetail inst={inst} />
                                ) : (
                                  displayName(inst)
                                )}
                              </td>
                              {!expanded && (
                                <>
                                  <td className="px-4 py-3 text-text-muted">
                                    {inst.assetClass || "—"}
                                  </td>
                                  <td className="px-4 py-3 text-text-muted">
                                    {inst.exchange || "—"}
                                  </td>
                                  <td className="px-4 py-3 text-text-muted">
                                    {inst.currency || "—"}
                                  </td>
                                  <td className="px-4 py-3">
                                    <span
                                      className={
                                        "inline-block rounded px-1.5 py-0.5 text-xs font-medium " +
                                        (identified
                                          ? "bg-primary-dark/10 text-primary-dark"
                                          : "bg-accent-soft/60 text-accent-dark")
                                      }
                                    >
                                      {identified
                                        ? "Identified"
                                        : "Unidentified"}
                                    </span>
                                  </td>
                                </>
                              )}
                            </tr>
                          );
                        })
                      )}
                    </tbody>
                  </table>
                </div>

                {(hasPrev || hasNext) && (
                  <div className="flex items-center justify-between pt-2">
                    <button
                      type="button"
                      onClick={goPrev}
                      disabled={!hasPrev}
                      className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm font-medium disabled:opacity-40 hover:enabled:bg-primary-light/15"
                    >
                      Previous
                    </button>
                    <span className="font-mono text-xs text-text-muted">
                      Page {pageIndex + 1}
                    </span>
                    <button
                      type="button"
                      onClick={goNext}
                      disabled={!hasNext}
                      className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm font-medium disabled:opacity-40 hover:enabled:bg-primary-light/15"
                    >
                      Next
                    </button>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </AppShell>
  );
}

function ExpandedDetail({ inst }: { inst: Instrument }) {
  const identified = isIdentified(inst);
  const canonicalIds = inst.identifiers.filter((id) => id.canonical);
  const brokerDescs = inst.identifiers.filter(
    (id) => id.type === IdentifierType.BROKER_DESCRIPTION
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-display text-base font-bold tracking-tight text-text-primary">
          {displayName(inst)}
        </span>
        <span
          className={
            "inline-block rounded px-1.5 py-0.5 text-xs font-medium " +
            (identified
              ? "bg-primary-dark/10 text-primary-dark"
              : "bg-accent-soft/60 text-accent-dark")
          }
        >
          {identified ? "Identified" : "Unidentified"}
        </span>
      </div>

      {/* Metadata row */}
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-text-muted">
        {inst.assetClass && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Class
            </span>{" "}
            {inst.assetClass}
          </span>
        )}
        {inst.exchange && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Exchange
            </span>{" "}
            {inst.exchange}
          </span>
        )}
        {inst.currency && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Currency
            </span>{" "}
            {inst.currency}
          </span>
        )}
        {inst.underlyingId && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Underlying
            </span>{" "}
            <span className="font-mono">{inst.underlyingId}</span>
          </span>
        )}
      </div>

      {/* Canonical identifiers */}
      {canonicalIds.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-text-muted">
            Identifiers
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {canonicalIds.map((id, i) => (
              <span
                key={i}
                className="inline-flex items-center gap-1 rounded bg-primary-dark/10 px-1.5 py-0.5 font-mono text-xs"
              >
                <span className="font-semibold text-primary-dark">
                  {idLabel(id)}
                </span>
                <span className="text-text-primary">{id.value}</span>
                {id.domain && (
                  <span className="text-text-muted">({id.domain})</span>
                )}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Broker descriptions */}
      {brokerDescs.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-text-muted">
            Broker Descriptions
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {brokerDescs.map((id, i) => (
              <span
                key={i}
                className="inline-flex items-center gap-1 rounded bg-accent-soft/30 px-1.5 py-0.5 font-mono text-xs"
              >
                <span className="text-text-primary">{id.value}</span>
                {id.domain && (
                  <span className="text-text-muted">({id.domain})</span>
                )}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
